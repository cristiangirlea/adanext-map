package mcpserver

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// holdExclusively opens path with no sharing, so that opening it again fails with a sharing
// violation, as it does at the instant another process renames a file over it. The returned
// function lets go.
func holdExclusively(t *testing.T, path string) (release func()) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			syscall.CloseHandle(h)
		}
	}
	t.Cleanup(release)
	return release
}

func TestLatestReadThatMeetsASharingViolationIsRetried(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	s := &stores{repo: repo}
	if _, err := s.get(); err != nil {
		t.Fatal(err)
	}

	writeStore(t, repo, commitB, sciptest.CallGraph())
	touchLatest(t, repo, time.Minute)
	release := holdExclusively(t, filepath.Join(repo, store.DirName, "latest"))
	pauses := 0
	s.pause = func(time.Duration) {
		pauses++
		if pauses == 2 { // the first retry still fails; the second finds latest free
			release()
		}
	}
	snap, err := s.get()
	if err != nil {
		t.Fatal(err)
	}
	if snap.header.Commit != commitB {
		t.Errorf("get while latest was held answered from %s, want it to retry and load %s", snap.header.Commit, commitB)
	}
	if pauses != 2 {
		t.Errorf("paused %d times, want 2", pauses)
	}
}

func TestLatestThatStaysHeldIsGivenUpOnAfterAFewAttempts(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	s := &stores{repo: repo}
	if _, err := s.get(); err != nil {
		t.Fatal(err)
	}

	writeStore(t, repo, commitB, sciptest.CallGraph())
	touchLatest(t, repo, time.Minute)
	holdExclusively(t, filepath.Join(repo, store.DirName, "latest"))
	pauses := 0
	s.pause = func(time.Duration) { pauses++ }
	snap, newer, err := s.current()
	if err != nil || newer == nil || snap.header.Commit != commitA {
		t.Fatalf("with latest held throughout, want the previous store serving and the failure reported: %v %v", newer, err)
	}
	if pauses != latestAttempts-1 {
		t.Errorf("paused %d times, want %d", pauses, latestAttempts-1)
	}
}
