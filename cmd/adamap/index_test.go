package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// fixtureFile writes the SCIP fixture to a file of its own and returns the path.
func fixtureFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, sciptest.Bytes(t, sciptest.Index()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runIndexCmd(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(append([]string{"index"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

// fakeIndexers replaces the indexer lookup and run for one test: found says which programs are on
// PATH, and each run writes the fixture as index.scip in its working directory.
func fakeIndexers(t *testing.T, found ...string) *[]string {
	t.Helper()
	var calls []string
	oldLook, oldRun := lookPath, runIndexer
	t.Cleanup(func() { lookPath, runIndexer = oldLook, oldRun })
	lookPath = func(name string) (string, error) {
		if slices.Contains(found, name) {
			return "/fake/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	runIndexer = func(dir, program string, args []string, _ io.Writer) error {
		calls = append(calls, program+" "+strings.Join(args, " "))
		return os.WriteFile(filepath.Join(dir, "index.scip"), sciptest.Bytes(t, sciptest.Index()), 0o644)
	}
	return &calls
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var summary = regexp.MustCompile(`^indexed 0123456789ab: 2 documents, 7 symbols, 9 occurrences in \S+ -> (.+)\n$`)

func TestIndexReadsAGivenSCIPFileIntoTheStore(t *testing.T) {
	repo := t.TempDir()
	code, out, errOut := runIndexCmd("--scip", fixtureFile(t), "--commit", testCommit, "--repo", repo)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	m := summary.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("stdout %q is not the summary line", out)
	}
	if want := store.Dir(repo, testCommit); m[1] != want {
		t.Errorf("summary names %s, want %s", m[1], want)
	}
	h, idx, err := store.Load(repo, testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if h.Adamap != Version || h.Indexer.Name != "scip-go" || h.Dirty || len(idx.Documents) != 2 {
		t.Errorf("header %+v, %d documents", h, len(idx.Documents))
	}
	if latest, err := store.Latest(repo); err != nil || latest != testCommit {
		t.Errorf("latest = %q, %v", latest, err)
	}
}

func TestIndexRunsTheIndexerTheRepositoryNeeds(t *testing.T) {
	for marker, want := range map[string]string{"go.mod": "/fake/bin/scip-go ", "Cargo.toml": "/fake/bin/rust-analyzer scip ."} {
		t.Run(marker, func(t *testing.T) {
			calls := fakeIndexers(t, "scip-go", "rust-analyzer")
			repo := t.TempDir()
			writeFile(t, filepath.Join(repo, marker), "")
			code, out, errOut := runIndexCmd("--commit", testCommit, "--repo", repo)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if !summary.MatchString(out) {
				t.Errorf("stdout %q is not the summary line", out)
			}
			if !slices.Equal(*calls, []string{want}) {
				t.Errorf("ran %q, want %q", *calls, want)
			}
			if _, err := os.Stat(filepath.Join(repo, "index.scip")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("index.scip was left behind: %v", err)
			}
		})
	}
}

func TestIndexNamesAMissingIndexerAndHowToInstallIt(t *testing.T) {
	cases := map[string][]string{
		"go.mod":     {"scip-go", "go install github.com/scip-code/scip-go/cmd/scip-go@latest"},
		"Cargo.toml": {"rust-analyzer", "rustup component add rust-analyzer"},
	}
	for marker, want := range cases {
		t.Run(marker, func(t *testing.T) {
			calls := fakeIndexers(t)
			repo := t.TempDir()
			writeFile(t, filepath.Join(repo, marker), "")
			code, out, errOut := runIndexCmd("--commit", testCommit, "--repo", repo)
			if code != 1 || out != "" {
				t.Fatalf("exit %d, stdout %q; want 1 and nothing", code, out)
			}
			for _, w := range want {
				if !strings.Contains(errOut, w) {
					t.Errorf("stderr %q does not mention %q", errOut, w)
				}
			}
			if len(*calls) != 0 {
				t.Errorf("ran %q", *calls)
			}
		})
	}
}

func TestIndexWithoutAProjectFileAsksForSCIP(t *testing.T) {
	fakeIndexers(t, "scip-go", "rust-analyzer")
	code, _, errOut := runIndexCmd("--commit", testCommit, "--repo", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "--scip") {
		t.Fatalf("exit %d, stderr %q; want 1 and a pointer to --scip", code, errOut)
	}
}

func TestIndexKeepsAnIndexSCIPItDidNotWrite(t *testing.T) {
	calls := fakeIndexers(t, "scip-go")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "")
	writeFile(t, filepath.Join(repo, "index.scip"), "mine")
	code, _, errOut := runIndexCmd("--commit", testCommit, "--repo", repo)
	if code != 1 || !strings.Contains(errOut, "index.scip") {
		t.Fatalf("exit %d, stderr %q; want 1 naming index.scip", code, errOut)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "index.scip")); string(data) != "mine" || len(*calls) != 0 {
		t.Errorf("index.scip now %q, indexer ran %q", data, *calls)
	}
}

func TestIndexRefusesAnIndexOverTheSizeLimit(t *testing.T) {
	repo := t.TempDir()
	code, _, errOut := runIndexCmd("--scip", fixtureFile(t), "--commit", testCommit, "--repo", repo, "--max-bytes", "10")
	if code != 1 || !strings.Contains(errOut, "--max-bytes") {
		t.Fatalf("exit %d, stderr %q; want 1 naming --max-bytes", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, ".adamap")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused index left a store: %v", err)
	}
}

func TestIndexRefusesAMalformedIndex(t *testing.T) {
	x := sciptest.Index()
	x.Documents[0].RelativePath = "../escape.go"
	path := filepath.Join(t.TempDir(), "bad.scip")
	if err := os.WriteFile(path, sciptest.Bytes(t, x), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runIndexCmd("--scip", path, "--commit", testCommit, "--repo", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "..") {
		t.Fatalf("exit %d, stderr %q; want 1 naming the bad path", code, errOut)
	}
}

func TestIndexUsageErrors(t *testing.T) {
	scip := fixtureFile(t)
	for _, args := range [][]string{
		{"--scip", scip, "--commit", "0123456"},
		{"--scip", scip, "--commit", testCommit, "--max-bytes", "0"},
		{"--scip", scip, "--commit", testCommit, "extra"},
		{"--bogus"},
	} {
		code, out, errOut := runIndexCmd(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: adamap index") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q; want 2 and the usage on stderr", args, code, out, errOut)
		}
	}
	code, out, _ := runIndexCmd("-h")
	if code != 0 || !strings.HasPrefix(out, "usage: adamap index") {
		t.Errorf("-h: exit %d, stdout %q", code, out)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestIndexTakesTheCommitFromGitAndMarksADirtyTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-q", "-m", "one")
	head := git(t, repo, "rev-parse", "HEAD")
	scip := fixtureFile(t)

	writeFile(t, filepath.Join(repo, "untracked.txt"), "not part of the tree\n")
	code, out, errOut := runIndexCmd("--scip", scip, "--repo", repo)
	if code != 0 || !strings.HasPrefix(out, "indexed "+head[:12]+":") {
		t.Fatalf("clean: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if h, _, err := store.Load(repo, head); err != nil || h.Dirty {
		t.Fatalf("clean: header %+v, %v; want a clean store of HEAD", h, err)
	}

	writeFile(t, filepath.Join(repo, "a.txt"), "two\n")
	code, out, errOut = runIndexCmd("--scip", scip, "--repo", repo)
	if code != 0 || !strings.HasPrefix(out, "indexed "+head[:12]+" (dirty):") {
		t.Fatalf("dirty: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if h, _, err := store.Load(repo, head); err != nil || !h.Dirty {
		t.Fatalf("dirty: header %+v, %v; want HEAD marked dirty", h, err)
	}
}

func TestIndexOutsideARepositoryAsksForCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	code, _, errOut := runIndexCmd("--scip", fixtureFile(t), "--repo", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "--commit") {
		t.Fatalf("exit %d, stderr %q; want 1 and a pointer to --commit", code, errOut)
	}
}
