package mcpserver

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/query"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// snapshot is one loaded store: its header, the model for the map, and the index for the queries.
// It is read-only once made, so calls may share it.
type snapshot struct {
	header store.Header
	idx    *model.Index
	x      *query.Index
}

// stores keeps the store the server answers from. A pinned commit is loaded once. Otherwise it
// follows .adamap/latest: at every call it compares latest's identity, modification time and size with those
// it saw when it last loaded, and loads again when they differ, so a new `adamap index` is answered
// from without a restart. store.Write rewrites latest after the store is complete, so a changed
// latest never names a store half written. When the store latest now names does not load, the
// previous one keeps serving, the failure is remembered against the stamp it was seen at, and no
// call loads again until the stamp changes.
type stores struct {
	repo, commit string

	mu     sync.Mutex
	cur    *snapshot
	stamp  stamp    // latest as it was when cur was loaded
	failed *failure // the newer store that did not load, if any

	pause  func(time.Duration)          // waits between attempts to read latest; nil is time.Sleep
	latest func(string) (string, error) // reads latest; nil is store.Latest
}

// stamp tracks latest's identity, time and size, and the served store's header time.
// Replacement identity detects re-indexing even when every timestamp is unchanged.
type stamp struct {
	file   os.FileInfo
	mod    time.Time
	size   int64
	header time.Time
}

func (a stamp) equal(b stamp) bool {
	// Unreadable identities leave metadata as the available evidence of a change.
	return a.mod.Equal(b.mod) && a.size == b.size && a.header.Equal(b.header) &&
		(a.file == nil || b.file == nil || os.SameFile(a.file, b.file))
}

// headerTime is the modification time of commit's header.json, or the zero time when it cannot be
// read, which differs from any real one.
func (s *stores) headerTime(commit string) time.Time {
	fi, err := os.Stat(filepath.Join(store.Dir(s.repo, commit), "header.json"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// failure is a newer store that did not load, and the stamp of latest when that was seen.
type failure struct {
	stamp stamp
	err   error
	// retry says the failure was in reading latest itself, which may pass at once (a rename in
	// progress): the next call tries again whatever the stamp.
	retry bool
}

// get is the store to answer from, loading it if it is not loaded yet or latest has changed. When
// there is no store to answer from the error says why (no store yet says to run `adamap index`),
// and the next call tries again.
func (s *stores) get() (*snapshot, error) {
	snap, _, err := s.current()
	return snap, err
}

// current is get, and also says why the store latest now names did not load, when one did not and
// the snapshot returned is the previous one.
func (s *stores) current() (snap *snapshot, newerErr, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commit != "" {
		if s.cur == nil {
			snap, err := load(s.repo, s.commit)
			if err != nil {
				return nil, nil, err
			}
			s.cur = snap
		}
		return s.cur, nil, nil
	}
	// Stat before reading: if latest changes in between, the next call sees a new stamp and loads
	// again, rather than this one keeping the new store under the old stamp's name.
	var now stamp
	fi, statErr := os.Lstat(filepath.Join(s.repo, store.DirName, "latest"))
	if statErr == nil {
		// Windows resolves file identity lazily, so capture it before the path can be replaced.
		now = stamp{mod: fi.ModTime(), size: fi.Size(), file: fi}
		if !os.SameFile(fi, fi) {
			now.file = nil
		}
		if s.cur != nil {
			now.header = s.headerTime(s.cur.header.Commit)
			if now.header.IsZero() {
				// store.Write removes the header first and writes it last, so no header means the
				// commit is being indexed again: wait for the new one rather than load a half store.
				now.header = s.stamp.header
			}
		}
	}
	if s.cur != nil {
		switch {
		case statErr != nil:
			// Latest has gone: keep answering from the store already loaded.
			return s.cur, nil, nil
		case now.equal(s.stamp):
			s.failed = nil
			return s.cur, nil, nil
		case s.failed != nil && !s.failed.retry && now.equal(s.failed.stamp):
			return s.cur, s.failed.err, nil
		}
	}
	commit, err := s.readLatest()
	if err != nil {
		return s.keep(err, now, true)
	}
	loadedAt := now
	loadedAt.header = s.headerTime(commit) // before the load, like latest's stamp
	snap, err = load(s.repo, commit)
	if err != nil {
		return s.keep(err, now, false)
	}
	s.cur, s.stamp, s.failed = snap, loadedAt, nil
	return snap, nil, nil
}

// keep is the answer to a newer store that failed with err, seen at stamp now. With a snapshot
// already loaded that keeps serving, and the failure is remembered; without one the error is the
// call's. retry is as in failure.
func (s *stores) keep(err error, now stamp, retry bool) (*snapshot, error, error) {
	if s.cur == nil {
		return nil, nil, err
	}
	s.failed = &failure{stamp: now, err: err, retry: retry}
	return s.cur, err, nil
}

// A rename of latest landing during a read fails it (on Windows the open itself, everywhere the
// check against the opened file). Such a read is tried again a few times, a few milliseconds apart.
const (
	latestAttempts = 5
	latestPause    = 5 * time.Millisecond
)

// readLatest retries Windows sharing violations, access denied and store.ErrReplaced.
func (s *stores) readLatest() (string, error) {
	pause := s.pause
	if pause == nil {
		pause = time.Sleep
	}
	latest := s.latest
	if latest == nil {
		latest = store.Latest
	}
	for attempt := 1; ; attempt++ {
		commit, err := latest(s.repo)
		if err == nil || attempt == latestAttempts || !(isSharingViolation(err) || errors.Is(err, store.ErrReplaced)) {
			return commit, err
		}
		pause(latestPause)
	}
}

func load(repo, commit string) (*snapshot, error) {
	h, idx, err := store.Load(repo, commit)
	if err != nil {
		return nil, err
	}
	return &snapshot{header: h, idx: idx, x: query.New(idx)}, nil
}
