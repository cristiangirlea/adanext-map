// Package store keeps adamap's model per commit under <repo>/.adamap/<commit>/: header.json, which
// says what the store holds and in which format, and store.json.gz, the model as gzip-compressed
// JSON. .adamap/latest names the commit written last.
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// FormatVersion is the store format this adamap writes and the newest it reads.
const FormatVersion = 1

// DirName is the directory under the repository root that holds the stores.
const DirName = ".adamap"

const (
	headerFile = "header.json"
	storeFile  = "store.json.gz"
	latestFile = "latest"

	gitignoreFile = ".gitignore"
)

// Header describes a store. Fields are only added, with omitempty.
type Header struct {
	Format int `json:"format"`
	// Adamap is the version of adamap that wrote the store.
	Adamap      string       `json:"adamap"`
	Commit      string       `json:"commit"`
	Dirty       bool         `json:"dirty,omitempty"`
	Indexer     model.Tool   `json:"indexer"`
	ProjectRoot string       `json:"project_root,omitempty"`
	Created     time.Time    `json:"created"`
	Counts      model.Counts `json:"counts"`
}

// Dir is the directory of commit's store in repo.
func Dir(repo, commit string) string { return filepath.Join(repo, DirName, commit) }

// CheckCommit accepts a full commit hash: 40 (SHA-1) or 64 (SHA-256) lower-case hex digits. The
// commit names a directory, so nothing else may pass.
func CheckCommit(c string) error {
	if len(c) != 40 && len(c) != 64 || strings.Trim(c, "0123456789abcdef") != "" {
		return fmt.Errorf("commit %q is not a full commit hash (40 or 64 lower-case hex digits)", c)
	}
	return nil
}

// Write stores idx for h.Commit in repo and points latest at it, returning the store's directory.
// It fills in h.Format and h.Counts. Each file is replaced atomically, and header.json goes last:
// a store without one is incomplete and does not load.
func Write(repo string, h Header, idx *model.Index) (string, error) {
	if err := CheckCommit(h.Commit); err != nil {
		return "", err
	}
	h.Format = FormatVersion
	h.Counts = idx.Counts()
	dir := Dir(repo, h.Commit)
	if err := checkDirs(repo, dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := ensureGitignore(filepath.Join(repo, DirName)); err != nil {
		return "", err
	}
	if err := os.Remove(filepath.Join(dir, headerFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	err := writeAtomic(dir, storeFile, func(w io.Writer) error {
		gz := gzip.NewWriter(w)
		if err := json.NewEncoder(gz).Encode(idx); err != nil {
			return err
		}
		return gz.Close()
	})
	if err != nil {
		return "", err
	}
	header, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeAtomic(dir, headerFile, writeBytes(append(header, '\n'))); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(repo, DirName), latestFile, writeBytes([]byte(h.Commit+"\n"))); err != nil {
		return "", err
	}
	return dir, nil
}

// checkDirs refuses a store directory that is a symbolic link or not a directory: a cloned
// repository can commit `.adamap` as a link, and writing through it would leave the repository.
// A directory that does not exist yet is fine.
func checkDirs(repo, dir string) error {
	for _, d := range []string{filepath.Join(repo, DirName), dir} {
		fi, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link: adamap will not read or write a store through one", d)
		case !fi.IsDir():
			return fmt.Errorf("%s is not a directory", d)
		}
	}
	return nil
}

// ensureGitignore gives dir a .gitignore of `*` unless it has one, so the stores show up in no
// repository's `git status` and cannot be committed by mistake. An existing file is the user's and
// is left as it is; a link, or anything but a regular file, is refused, as git does not read one.
func ensureGitignore(dir string) error {
	path := filepath.Join(dir, gitignoreFile)
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return writeAtomic(dir, gitignoreFile, writeBytes([]byte("*\n")))
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link: adamap will not write a store beside one", path)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// createNew writes content to dir/name only if nothing is there: the file is completed under a
// temporary name first, so a reader never sees half of it, and then published without replacing
// whatever appeared at the path since the caller looked.
func createNew(dir, name string, content []byte) error {
	tmp, err := writeTemp(dir, name, writeBytes(content))
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return publishNew(tmp, filepath.Join(dir, name), content)
}

// publishNew links tmp to path, which fails if path exists; a file already there is the user's and
// counts as success. A filesystem that cannot link gets the same guarantee from an exclusive create.
func publishNew(tmp, path string, content []byte) error {
	err := os.Link(tmp, path)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return nil
	}
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(openErr, fs.ErrExist) {
		return nil
	}
	if openErr != nil {
		return fmt.Errorf("%w (and %v)", err, openErr)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeBytes(b []byte) func(io.Writer) error {
	return func(w io.Writer) error { _, err := w.Write(b); return err }
}

// writeAtomic writes name in dir through a temporary file in the same directory, renamed into
// place once complete, so a reader sees the old file or the new one, never half of one.
func writeAtomic(dir, name string, write func(io.Writer) error) error {
	tmp, err := writeTemp(dir, name, write)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeTemp writes name's content to a synced temporary file in dir and returns its path.
func writeTemp(dir, name string, write func(io.Writer) error) (tmp string, err error) {
	f, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if err = write(f); err != nil {
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// MaxLoadBytes is how much Load decompresses before giving up: a repository can carry a planted
// store.json.gz that inflates without end.
const MaxLoadBytes = 4 << 30

// Load reads commit's store from repo.
func Load(repo, commit string) (Header, *model.Index, error) {
	return LoadLimited(repo, commit, MaxLoadBytes)
}

// LoadLimited is Load with a limit on the decompressed bytes it reads.
func LoadLimited(repo, commit string, limit int64) (Header, *model.Index, error) {
	if err := CheckCommit(commit); err != nil {
		return Header{}, nil, err
	}
	dir := Dir(repo, commit)
	if err := checkDirs(repo, dir); err != nil {
		return Header{}, nil, err
	}
	data, err := readHeader(filepath.Join(dir, headerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Header{}, nil, fmt.Errorf("no store for commit %s in %s: run `adamap index`", commit, repo)
	}
	if err != nil {
		return Header{}, nil, err
	}
	var h Header
	if err := json.Unmarshal(data, &h); err != nil {
		return Header{}, nil, fmt.Errorf("%s: %w; rebuild it with `adamap index`", filepath.Join(dir, headerFile), err)
	}
	switch {
	case h.Format < 1:
		return Header{}, nil, fmt.Errorf("%s has no format version: rebuild it with `adamap index`", dir)
	case h.Format > FormatVersion:
		need := "a newer adamap"
		if h.Adamap != "" {
			need = "adamap " + h.Adamap + " or later"
		}
		return Header{}, nil, fmt.Errorf("the store in %s is format %d; this adamap reads format %d: "+
			"use %s, or rebuild it with `adamap index`", dir, h.Format, FormatVersion, need)
	case h.Commit != commit:
		return Header{}, nil, fmt.Errorf("the store in %s says it is commit %q: rebuild it with `adamap index`", dir, h.Commit)
	}
	if err := checkRegular(filepath.Join(dir, storeFile)); err != nil {
		return Header{}, nil, err
	}
	f, err := os.Open(filepath.Join(dir, storeFile))
	if err != nil {
		return Header{}, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return Header{}, nil, fmt.Errorf("%s: %w", filepath.Join(dir, storeFile), err)
	}
	var idx model.Index
	if err := json.NewDecoder(&limitedReader{r: gz, left: limit, limit: limit}).Decode(&idx); err != nil {
		return Header{}, nil, fmt.Errorf("%s: %w", filepath.Join(dir, storeFile), err)
	}
	for i, d := range idx.Documents {
		if err := model.CheckPath(d.Path); err != nil {
			return Header{}, nil, fmt.Errorf("%s: document %d: %w", filepath.Join(dir, storeFile), i, err)
		}
		for j, s := range d.Locals {
			if err := checkDefinition(s); err != nil {
				return Header{}, nil, fmt.Errorf("%s: document %d local %d: %w", filepath.Join(dir, storeFile), i, j, err)
			}
		}
	}
	for i, s := range idx.Symbols {
		if err := checkDefinition(s); err != nil {
			return Header{}, nil, fmt.Errorf("%s: symbol %d: %w", filepath.Join(dir, storeFile), i, err)
		}
	}
	return h, &idx, nil
}

// checkDefinition checks the path of s's definition, when it has one.
func checkDefinition(s model.Symbol) error {
	if s.Definition == nil {
		return nil
	}
	return model.CheckPath(s.Definition.Path)
}

// maxHeaderBytes is the largest header.json Load reads; a real one is a few hundred bytes.
const maxHeaderBytes = 1 << 20

// checkRegular refuses path unless it is a regular file, not a link to one: a cloned repository can
// commit a store file as a symbolic link to a device or to a file outside itself.
func checkRegular(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: adamap will not read a store through a link or a device", path)
	}
	return nil
}

// readHeader reads header.json, at most maxHeaderBytes of it.
func readHeader(path string) ([]byte, error) {
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxHeaderBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxHeaderBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes: rebuild the store with `adamap index`", path, maxHeaderBytes)
	}
	return data, nil
}

// limitedReader reads at most left bytes of r and fails, rather than reporting EOF, past that.
type limitedReader struct {
	r     io.Reader
	left  int64
	limit int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left <= 0 {
		// Only more data is an error: a store of exactly the limit is fine.
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("decompresses to more than the limit of %d bytes", l.limit)
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

// Latest is the commit whose store was written last in repo.
func Latest(repo string) (string, error) {
	if err := checkDirs(repo, filepath.Join(repo, DirName)); err != nil {
		return "", err
	}
	path := filepath.Join(repo, DirName, latestFile)
	if err := checkRegular(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("no store in %s yet: run `adamap index` (%w)", repo, err)
		}
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("no store in %s yet: run `adamap index` (%w)", repo, err)
	}
	defer f.Close()
	// One byte past the limit tells a file of exactly the limit from a longer one.
	data, err := io.ReadAll(io.LimitReader(f, maxLatestBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxLatestBytes {
		return "", fmt.Errorf("%s is longer than %d bytes, so it is not a commit: run `adamap index`", path, maxLatestBytes)
	}
	c := string(bytes.TrimSpace(data))
	if err := CheckCommit(c); err != nil {
		shown := c
		if len(shown) > maxQuotedBytes {
			shown = shown[:maxQuotedBytes] + "..."
		}
		return "", fmt.Errorf("%s holds %q, which is not a commit: run `adamap index`", path, shown)
	}
	return c, nil
}

const (
	// maxLatestBytes is how much of .adamap/latest Latest reads; a commit and a newline fit in 65.
	maxLatestBytes = 128
	// maxQuotedBytes is how much of a bad latest an error quotes.
	maxQuotedBytes = 40
)
