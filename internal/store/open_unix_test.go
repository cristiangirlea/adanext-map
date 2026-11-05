//go:build unix

package store

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularRefusesFIFOWithoutBlocking(t *testing.T) {
	for _, swapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "swapped"}[swapped], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), latestFile)
			if swapped {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				realOpen := openFile
				useOpenFile(t, func(path string) (*os.File, error) {
					if err := os.Remove(path); err != nil {
						return nil, err
					}
					if err := syscall.Mkfifo(path, 0o600); err != nil {
						return nil, err
					}
					return realOpen(path)
				})
			} else if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				f, err := openRegular(path)
				if f != nil {
					f.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "not a regular file") {
					t.Fatalf("openRegular = %v, want regular-file refusal", err)
				}
			case <-time.After(2 * time.Second):
				// Opening both ends releases a blocking reader before the test cleans up.
				f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err == nil {
					defer f.Close()
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
				}
				t.Fatal("openRegular blocked on a FIFO")
			}
		})
	}
}
