package mcpserver

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
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

// requireSharingEnforced skips the test where an open still succeeds while another handle holds the
// file with no sharing: GitHub's Windows runners do (their account opens with backup privileges, it
// seems), so no sharing violation can be made there to test the retry against.
func requireSharingEnforced(t *testing.T) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := holdExclusively(t, p)
	defer release()
	if f, err := os.Open(p); err == nil {
		f.Close()
		t.Skip("this machine opens a file another handle holds with no sharing, so no sharing violation can be made here")
	}
}

func TestLatestReadThatMeetsASharingViolationIsRetried(t *testing.T) {
	requireSharingEnforced(t)
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
	requireSharingEnforced(t)
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

func TestLatestRenameRetryErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{syscall.ERROR_ACCESS_DENIED, true},
		{errorSharingViolation, true},
		{&os.PathError{Op: "open", Path: "latest", Err: syscall.ERROR_ACCESS_DENIED}, true},
		{syscall.ERROR_FILE_NOT_FOUND, false},
		{nil, false},
	} {
		if got := isSharingViolation(tc.err); got != tc.want {
			t.Errorf("retry(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestLatestReadAccessDeniedIsRetried(t *testing.T) {
	for _, recover := range []bool{true, false} {
		name := "persistent"
		if recover {
			name = "transient"
		}
		t.Run(name, func(t *testing.T) {
			attempts, pauses := 0, 0
			denied := &os.PathError{Op: "open", Path: "latest", Err: syscall.ERROR_ACCESS_DENIED}
			s := &stores{
				latest: func(string) (string, error) {
					attempts++
					if recover && attempts == 3 {
						return commitA, nil
					}
					return "", denied
				},
				pause: func(d time.Duration) {
					pauses++
					if d != latestPause {
						t.Errorf("pause = %v, want %v", d, latestPause)
					}
				},
			}
			commit, err := s.readLatest()
			wantAttempts := latestAttempts
			if recover {
				wantAttempts = 3
				if err != nil || commit != commitA {
					t.Errorf("readLatest = %q, %v, want %q, nil", commit, err, commitA)
				}
			} else if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) || commit != "" {
				t.Errorf("readLatest = %q, %v, want empty commit and access denied", commit, err)
			}
			if attempts != wantAttempts || pauses != wantAttempts-1 {
				t.Errorf("attempts=%d, pauses=%d, want %d, %d", attempts, pauses, wantAttempts, wantAttempts-1)
			}
		})
	}
}

func TestRealLatestRenameErrors(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	path := filepath.Join(repo, store.DirName, "latest")
	counts := map[syscall.Errno]int{}
	replaced := 0
	s := &stores{repo: repo, latest: func(repo string) (string, error) {
		commit, err := store.Latest(repo)
		if err == nil {
			counts[0]++
		} else {
			var errno syscall.Errno
			if errors.As(err, &errno) {
				counts[errno]++
			} else if errors.Is(err, store.ErrReplaced) {
				replaced++
			} else {
				t.Errorf("unexpected latest error: %v", err)
			}
		}
		return commit, err
	}}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	ready := make(chan struct{})
	var writeErr error
	renames, renameFailures := 0, 0
	wg.Go(func() {
		var signal sync.Once
		defer signal.Do(func() { close(ready) })
		for {
			select {
			case <-stop:
				return
			default:
			}
			tmp := path + ".replacement"
			if err := os.WriteFile(tmp, []byte(commitA+"\n"), 0o644); err != nil {
				writeErr = err
				return
			}
			if err := os.Rename(tmp, path); err != nil {
				renameFailures++
			} else {
				renames++
			}
			signal.Do(func() { close(ready) })
			// Leave time for a retry to observe a completed replacement.
			time.Sleep(time.Millisecond)
		}
	})
	<-ready
	for i := 0; i < 200; i++ {
		if commit, err := s.readLatest(); err != nil || commit != commitA {
			t.Errorf("readLatest iteration %d = %q, %v, want %q, nil", i, commit, err, commitA)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("real rename reads: success=%d, ERROR_ACCESS_DENIED=%d, ERROR_SHARING_VIOLATION=%d, replaced=%d; renames=%d, rename failures=%d", counts[0], counts[syscall.ERROR_ACCESS_DENIED], counts[errorSharingViolation], replaced, renames, renameFailures)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if renames == 0 {
		t.Error("no replacement succeeded during concurrent reads")
	}
}
