package mcpserver

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestDiagSharing logs what opening a file held with no sharing returns on this machine.
func TestDiagSharing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "latest")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, _ := syscall.UTF16PtrFromString(p)
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	t.Logf("dir=%s CreateFile(share 0) err=%v", dir, err)
	defer syscall.CloseHandle(h)
	f, err := os.Open(p)
	t.Logf("os.Open while held: err=%v (%#v)", err, err)
	if f != nil {
		f.Close()
	}
	_, err = os.ReadFile(p)
	t.Logf("os.ReadFile while held: err=%v", err)
	fi, err := os.Lstat(p)
	t.Logf("os.Lstat while held: err=%v size=%d", err, func() int64 {
		if fi != nil {
			return fi.Size()
		}
		return -1
	}())
	t.Logf("TEMP=%s TMP=%s", os.Getenv("TEMP"), os.Getenv("TMP"))
}
