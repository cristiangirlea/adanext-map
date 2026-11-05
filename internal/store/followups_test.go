package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

func TestDecodeQuotesUnexpectedTokens(t *testing.T) {
	for _, bad := range []string{"escape\x1b[2J", "bidi\u202e", "line\nbreak"} {
		token, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []string{string(token), `{"documents":` + string(token) + `}`, `{"symbols":[` + string(token) + `]}`} {
			_, err := decodeIndex(strings.NewReader(input), model.DefaultLimits())
			if err == nil || !strings.Contains(err.Error(), strconv.Quote(bad)) || strings.Contains(err.Error(), bad) {
				t.Errorf("decodeIndex(%s) = %v, want quoted token", input, err)
			}
		}
	}
}

func TestDecodeUnexpectedTokenDisplay(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`[]`, "expected an object, found ["},
		{`"text"`, `expected an object, found "text"`},
		{`{"documents":{}}`, "expected an array, found {"},
		{`{"documents":"text"}`, `expected an array, found "text"`},
	} {
		_, err := decodeIndex(strings.NewReader(tc.input), model.DefaultLimits())
		if err == nil || err.Error() != tc.want {
			t.Errorf("decodeIndex(%s) = %v, want %s", tc.input, err, tc.want)
		}
	}
}

func TestOpenRegularRefusesReplacementAtSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	useOpenFile(t, func(path string) (*os.File, error) {
		if err := os.Rename(path, path+".old"); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return nil, err
		}
		return os.Open(path)
	})
	f, err := openRegular(path)
	if f != nil {
		f.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("openRegular = %v, want replacement refusal", err)
	}
}

func TestOpenRegularRefusesDirectorySwappedIntoPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var opened *os.File
	useOpenFile(t, func(path string) (*os.File, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return nil, err
		}
		var err error
		opened, err = os.Open(path)
		return opened, err
	})
	f, err := openRegular(path)
	if f != nil {
		f.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("openRegular = %v, want not a regular file", err)
	}
	if opened == nil {
		t.Fatal("directory was not opened")
	}
	if _, err := opened.Stat(); err == nil {
		t.Error("rejected file was not closed")
	}
}

func TestNewStoreFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	repo := t.TempDir()
	dir, err := Write(repo, header(commitA), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(repo, DirName, latestFile), filepath.Join(dir, headerFile), filepath.Join(dir, storeFile)} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s mode = %o, want private", path, got)
		}
	}
}

func TestPublishNewFallbackGivesAnOrdinaryFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "new")
	if err := publishNew(filepath.Join(dir, "missing"), path, []byte("*\n")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The umask may take bits away, never add them.
	if got := fi.Mode().Perm(); got&^0o644 != 0 || got&0o600 != 0o600 {
		t.Errorf("fallback mode = %o, want 644 less the umask", got)
	}
}
