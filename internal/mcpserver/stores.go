package mcpserver

import (
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
// follows .adamap/latest: at every call it compares latest's modification time and size with those
// it saw when it last loaded, and loads again when they differ, so a new `adamap index` is answered
// from without a restart. store.Write rewrites latest after the store is complete, so a changed
// latest never names a store half written.
type stores struct {
	repo, commit string

	mu    sync.Mutex
	cur   *snapshot
	stamp stamp // latest as it was when cur was loaded
}

// stamp is what tells one version of .adamap/latest from the next.
type stamp struct {
	mod  time.Time
	size int64
}

func (a stamp) equal(b stamp) bool { return a.mod.Equal(b.mod) && a.size == b.size }

// get is the store to answer from, loading it if it is not loaded yet or latest has changed. When
// loading fails the error says why (no store yet says to run `adamap index`), and the next call
// tries again.
func (s *stores) get() (*snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commit != "" {
		if s.cur == nil {
			snap, err := load(s.repo, s.commit)
			if err != nil {
				return nil, err
			}
			s.cur = snap
		}
		return s.cur, nil
	}
	// Stat before reading: if latest changes in between, the next call sees a new stamp and loads
	// again, rather than this one keeping the new store under the old stamp's name.
	var now stamp
	fi, statErr := os.Lstat(filepath.Join(s.repo, store.DirName, "latest"))
	if statErr == nil {
		now = stamp{fi.ModTime(), fi.Size()}
	}
	if s.cur != nil && (statErr != nil || now.equal(s.stamp)) {
		// Unchanged, or latest has gone: keep answering from the store already loaded.
		return s.cur, nil
	}
	commit, err := store.Latest(s.repo)
	if err != nil {
		return nil, err
	}
	snap, err := load(s.repo, commit)
	if err != nil {
		return nil, err
	}
	s.cur, s.stamp = snap, now
	return snap, nil
}

func load(repo, commit string) (*snapshot, error) {
	h, idx, err := store.Load(repo, commit)
	if err != nil {
		return nil, err
	}
	return &snapshot{header: h, idx: idx, x: query.New(idx)}, nil
}
