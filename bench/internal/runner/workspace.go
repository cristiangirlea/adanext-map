package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

// gitSafely goes in front of every git command the runner runs, as in cmd/adamap: a repository's
// own config and hooks must not run programs (core.fsmonitor, the hooks path).
var gitSafely = []string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
	"-c", "core.autocrlf=false", "-c", "advice.detachedHead=false"}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", slices.Concat([]string{"-C", dir}, gitSafely, args)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return out.String(), nil
}

// Checkout makes dir a fresh repository holding source's revision and nothing else: one commit,
// fetched shallow by its hash, no remote, no branch or tag, so no later commit (the fix
// included) is reachable from it.
func Checkout(source, revision, dir string) error {
	if strings.HasPrefix(source, "-") {
		return fmt.Errorf("source %q starts with -, which git would read as an option", source)
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s exists: every run starts in a fresh workspace", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := git(dir, "init", "-q"); err != nil {
		return err
	}
	if err := fetch(dir, source, revision); err != nil {
		return err
	}
	if _, err := git(dir, "checkout", "-q", "--detach", revision); err != nil {
		return err
	}
	head, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != revision {
		return fmt.Errorf("the workspace is at %s, not %s", strings.TrimSpace(head), revision)
	}
	return os.Remove(filepath.Join(dir, ".git", "FETCH_HEAD"))
}

// fetch takes one commit by its hash, shallow. A local source serves any commit it has; a
// hosted one (GitHub) serves a reachable commit by hash.
func fetch(dir, source, revision string) error {
	args := []string{"fetch", "-q", "--depth", "1", "--no-tags"}
	if !strings.Contains(source, "://") && !strings.HasPrefix(source, "git@") {
		args = append(args, "--upload-pack", "git -c uploadpack.allowAnySHA1InWant=true upload-pack")
	}
	_, err := git(dir, append(args, source, revision)...)
	return err
}

// reset puts the workspace back on revision as a fresh checkout has it, tracked and untracked
// files alike, keeping the paths under keep (a condition's index).
func reset(dir, revision string, keep []string) error {
	if _, err := git(dir, "reset", "-q", "--hard", revision); err != nil {
		return err
	}
	args := []string{"clean", "-q", "-ffdx"}
	for _, k := range keep {
		args = append(args, "-e", "/"+strings.TrimSuffix(path.Clean(filepath.ToSlash(k)), "/"))
	}
	_, err := git(dir, args...)
	return err
}

// Reachable reports whether rev names a commit present in the repository at dir.
func Reachable(dir, rev string) bool {
	_, err := git(dir, "cat-file", "-e", rev+"^{commit}")
	return err == nil
}

// Fingerprint is the tree at dir: its commit and the hash of every difference from it, tracked
// and untracked, by content. Paths under exclude (a condition's index) do not count.
func Fingerprint(dir string, exclude []string) (record.Fingerprint, error) {
	head, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return record.Fingerprint{}, err
	}
	out, err := git(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=no")
	if err != nil {
		return record.Fingerprint{}, err
	}
	var entries []record.Entry
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		status, p := f[:2], f[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++ // the original path follows a rename or copy
		}
		if excluded(p, exclude) {
			continue
		}
		sum, err := fileHash(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return record.Fingerprint{}, err
		}
		entries = append(entries, record.Entry{Status: status, Path: p, SHA256: sum})
	}
	return record.Fingerprint{Commit: strings.TrimSpace(head), Content: record.ContentHash(entries)}, nil
}

func excluded(p string, exclude []string) bool {
	for _, e := range exclude {
		e = strings.TrimSuffix(path.Clean(filepath.ToSlash(e)), "/")
		if p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
	}
	return false
}

// fileHash is the content hash of a file, its target for a link, "" for a missing file.
func fileHash(p string) (string, error) {
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	h := sha256.New()
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return "", err
		}
		io.WriteString(h, "link:"+target)
	case fi.Mode().IsRegular():
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	default:
		io.WriteString(h, "mode:"+fi.Mode().String())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// digestTree is the hash of every file under root, by path and content, and their total size.
func digestTree(root string) (string, int64, error) {
	type file struct{ rel, sum string }
	var files []file
	var size int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum, err := fileHash(p)
		if err != nil {
			return err
		}
		if fi, err := d.Info(); err == nil {
			size += fi.Size()
		}
		files = append(files, file{filepath.ToSlash(rel), sum})
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	if len(files) == 0 {
		return "", 0, fmt.Errorf("%s holds no files", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\n", f.rel, f.sum)
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// copyTree copies the regular files under src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// fixFiles lists the files the fix changed or added since revision that match the patterns
// (default *_test.go), from a grader checkout that holds both commits.
func fixFiles(grader, revision, fix string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		patterns = []string{"*_test.go"}
	}
	out, err := git(grader, "diff", "--name-only", "--no-renames", "--diff-filter=AM", "-z", revision, fix)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		for _, pat := range patterns {
			full, _ := path.Match(pat, p)
			base, _ := path.Match(pat, path.Base(p))
			if full || base {
				files = append(files, p)
				break
			}
		}
	}
	return files, nil
}
