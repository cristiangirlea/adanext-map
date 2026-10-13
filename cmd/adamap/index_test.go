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
	"time"

	scippb "github.com/scip-code/scip/bindings/go/scip"

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

// outputFlag is the argument that names where an indexer writes its index.
const outputFlag = "--output"

// outputOf is the path after outputFlag in an indexer's arguments, or "" when there is none.
func outputOf(args []string) string {
	if i := slices.Index(args, outputFlag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// fakeIndexers replaces the indexer lookup and run for one test: found says which programs are on
// PATH, and each run writes the fixture where its --output says. A call is recorded as the program
// and its arguments with the output path shown as OUT.
func fakeIndexers(t *testing.T, found ...string) *[]string {
	calls, _ := fakeIndexersOut(t, found...)
	return calls
}

// fakeIndexersOut is fakeIndexers that also returns the directory of each run's output, resolved
// while it exists, since adamap removes it afterwards.
func fakeIndexersOut(t *testing.T, found ...string) (calls, outDirs *[]string) {
	t.Helper()
	calls, outDirs = new([]string), new([]string)
	oldLook, oldRun := lookPath, runIndexer
	t.Cleanup(func() { lookPath, runIndexer = oldLook, oldRun })
	lookPath = func(name string) (string, error) {
		if slices.Contains(found, name) {
			return "/fake/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	runIndexer = func(_, program string, args []string, _ io.Writer) error {
		out := outputOf(args)
		if out == "" {
			t.Errorf("%s ran without %s: %q", program, outputFlag, args)
			return errors.New("no output path")
		}
		shown := slices.Clone(args)
		shown[slices.Index(shown, outputFlag)+1] = "OUT"
		*calls = append(*calls, program+" "+strings.Join(shown, " "))
		dir, err := filepath.EvalSymlinks(filepath.Dir(out))
		if err != nil {
			t.Errorf("the output directory does not exist when the indexer runs: %v", err)
			return err
		}
		*outDirs = append(*outDirs, dir)
		return os.WriteFile(out, sciptest.Bytes(t, sciptest.Index()), 0o644)
	}
	return calls, outDirs
}

// within reports whether path is dir or below it, after resolving symbolic links and 8.3 short
// names in dir, which must exist.
func within(t *testing.T, dir, path string) bool {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(resolved, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// gone fails the test when path still exists.
func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s is still there: %v", path, err)
	}
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
	for marker, want := range map[string]string{
		"go.mod":     "/fake/bin/scip-go --output OUT",
		"Cargo.toml": "/fake/bin/rust-analyzer scip . --output OUT",
	} {
		t.Run(marker, func(t *testing.T) {
			calls, outDirs := fakeIndexersOut(t, "scip-go", "rust-analyzer")
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
			// The index is written outside the repository and its directory is removed afterwards.
			if len(*outDirs) != 1 {
				t.Fatalf("indexer ran %d times, want once", len(*outDirs))
			}
			if within(t, repo, (*outDirs)[0]) {
				t.Errorf("the indexer wrote inside the repository: %s", (*outDirs)[0])
			}
			gone(t, (*outDirs)[0])
		})
	}
}

func TestIndexPrefersCargoTomlWhenBothProjectFilesExist(t *testing.T) {
	calls := fakeIndexers(t, "scip-go", "rust-analyzer")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "")
	writeFile(t, filepath.Join(repo, "Cargo.toml"), "")
	if code, _, errOut := runIndexCmd("--commit", testCommit, "--repo", repo); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !slices.Equal(*calls, []string{"/fake/bin/rust-analyzer scip . --output OUT"}) {
		t.Errorf("ran %q, want rust-analyzer", *calls)
	}
}

// When the indexer fails or writes an index adamap refuses, the run fails, no store is written,
// and the directory it wrote its index in is removed.
func TestIndexCleansUpAfterAFailedIndexer(t *testing.T) {
	bad := sciptest.Index()
	bad.Documents[0].Occurrences[0].Range = []int32{5, 0, 4, 9}
	cases := []struct {
		name  string
		write func(out string) error
		args  []string
		want  string
	}{
		{"indexer exits non-zero", func(out string) error {
			writeFile(t, out, "partial")
			return errors.New("exit status 101")
		}, nil, "scip-go failed: exit status 101"},
		{"indexer writes a malformed index", func(out string) error {
			return os.WriteFile(out, sciptest.Bytes(t, bad), 0o644)
		}, nil, "ends before"},
		{"indexer writes too much", func(out string) error {
			return os.WriteFile(out, sciptest.Bytes(t, sciptest.Index()), 0o644)
		}, []string{"--max-bytes", "10"}, "--max-bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeIndexers(t, "scip-go")
			var outDir string
			runIndexer = func(_, _ string, args []string, _ io.Writer) error {
				outDir = filepath.Dir(outputOf(args))
				return c.write(outputOf(args))
			}
			repo := t.TempDir()
			writeFile(t, filepath.Join(repo, "go.mod"), "")
			code, out, errOut := runIndexCmd(append([]string{"--commit", testCommit, "--repo", repo}, c.args...)...)
			if code != 1 || out != "" || !strings.Contains(errOut, c.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 1 and %q", code, out, errOut, c.want)
			}
			for _, name := range []string{"index.scip", ".adamap"} {
				gone(t, filepath.Join(repo, name))
			}
			if outDir == "" {
				t.Fatal("the indexer was not run with an output path")
			}
			gone(t, outDir)
		})
	}
}

func TestIndexAddsRustupHintWhenRustAnalyzerFails(t *testing.T) {
	cases := []struct {
		name     string
		marker   string
		program  string
		wantHint bool
	}{
		{"rust-analyzer", "Cargo.toml", "rust-analyzer", true},
		{"scip-go", "go.mod", "scip-go", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeIndexers(t, "rust-analyzer", "scip-go")
			runIndexer = func(dir, program string, args []string, _ io.Writer) error {
				return errors.New("exit status 1")
			}
			repo := t.TempDir()
			writeFile(t, filepath.Join(repo, c.marker), "")
			code, out, errOut := runIndexCmd("--commit", testCommit, "--repo", repo)
			if code != 1 || out != "" {
				t.Fatalf("exit %d, stdout %q; want 1 and nothing", code, out)
			}
			if c.wantHint {
				if !strings.Contains(errOut, "rustup component add rust-analyzer") {
					t.Errorf("stderr %q does not contain rustup hint for rust-analyzer", errOut)
				}
			} else {
				if strings.Contains(errOut, "rustup component add") {
					t.Errorf("stderr %q contains rustup hint but should not for %s", errOut, c.name)
				}
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

// An index.scip already in the repository is the user's: the indexer writes elsewhere, so it is
// neither refused, nor read in place of the indexer's output, nor deleted.
func TestIndexLeavesAnIndexSCIPInTheRepositoryAlone(t *testing.T) {
	calls := fakeIndexers(t, "scip-go")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "")
	writeFile(t, filepath.Join(repo, "index.scip"), "mine")
	code, _, errOut := runIndexCmd("--commit", testCommit, "--repo", repo)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q; want success", code, errOut)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "index.scip")); string(data) != "mine" || len(*calls) != 1 {
		t.Errorf("index.scip now %q, indexer ran %q", data, *calls)
	}
	if _, idx, err := store.Load(repo, testCommit); err != nil || len(idx.Documents) != 2 {
		t.Errorf("the store holds the indexer's output, not the file in the repository: %v, %v", idx, err)
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
	x.Documents[0].Occurrences[0].Range = []int32{5, 0, 4, 9}
	path := filepath.Join(t.TempDir(), "bad.scip")
	if err := os.WriteFile(path, sciptest.Bytes(t, x), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runIndexCmd("--scip", path, "--commit", testCommit, "--repo", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "ends before") {
		t.Fatalf("exit %d, stderr %q; want 1 naming the bad range", code, errOut)
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
	dir := t.TempDir()
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", parent) // a temp dir inside a work tree is still no repository
	t.Setenv("GIT_DIR", "")
	code, _, errOut := runIndexCmd("--scip", fixtureFile(t), "--repo", dir)
	if code != 1 || !strings.Contains(errOut, "--commit") {
		t.Fatalf("exit %d, stderr %q; want 1 and a pointer to --commit", code, errOut)
	}
}

func TestIndexSkipsDocumentsOutsideTheProjectAndSaysSo(t *testing.T) {
	x := sciptest.Index()
	x.Documents = append(x.Documents, &scippb.Document{RelativePath: `..\..\go-build\d7\abc-d`, Language: "go"},
		&scippb.Document{RelativePath: "/abs.go"})
	path := filepath.Join(t.TempDir(), "outside.scip")
	if err := os.WriteFile(path, sciptest.Bytes(t, x), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	code, out, errOut := runIndexCmd("--scip", path, "--commit", testCommit, "--repo", repo)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(errOut, "skipped 2 documents adamap cannot store (outside the project or not a safe file name)") || !strings.Contains(errOut, `go-build`) {
		t.Errorf("stderr %q does not say two documents were skipped", errOut)
	}
	if !summary.MatchString(out) {
		t.Errorf("stdout %q is not the summary line", out)
	}
	if _, idx, err := store.Load(repo, testCommit); err != nil || len(idx.Documents) != 2 {
		t.Errorf("store: %v, %v", idx, err)
	}
}

func TestIndexSaysNothingWhenNothingIsSkipped(t *testing.T) {
	_, _, errOut := runIndexCmd("--scip", fixtureFile(t), "--commit", testCommit, "--repo", t.TempDir())
	if strings.Contains(errOut, "skipped") {
		t.Errorf("stderr %q mentions a skip", errOut)
	}
}

// A repository's own .git/config and .git/hooks are its owner's to write, so indexing one must not
// run what they name. Each case arms a program that leaves a marker file, checks that a plain git
// does run it (otherwise the case proves nothing on this git), then indexes and wants no marker.
func TestIndexDoesNotRunProgramsNamedByTheRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	cases := []struct {
		name string
		arm  func(t *testing.T, repo, marker string)
	}{
		{"core.fsmonitor", func(t *testing.T, repo, marker string) {
			// git runs the value through sh, on Windows too (Git for Windows bundles it).
			git(t, repo, "config", "core.fsmonitor", "echo x > '"+filepath.ToSlash(marker)+"'; false")
		}},
		{"post-index-change hook", func(t *testing.T, repo, marker string) {
			hook := "#!/bin/sh\necho x > '" + filepath.ToSlash(marker) + "'\n"
			hooks := filepath.Join(repo, ".git", "hooks") // absent when git has no templates
			if err := os.MkdirAll(hooks, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(hooks, "post-index-change"), []byte(hook), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			marker := filepath.Join(t.TempDir(), "ran")
			git(t, repo, "init", "-q")
			writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
			git(t, repo, "add", "a.txt")
			git(t, repo, "commit", "-q", "-m", "one")
			c.arm(t, repo, marker)
			// The hook runs only when status rewrites the index, which it does for a file whose
			// timestamp changed but whose content did not.
			later := time.Now().Add(time.Minute)
			if err := os.Chtimes(filepath.Join(repo, "a.txt"), later, later); err != nil {
				t.Fatal(err)
			}

			git(t, repo, "status", "--porcelain")
			if _, err := os.Stat(marker); err != nil {
				t.Skipf("a plain git status does not run %s here (%v), so there is nothing to guard against", c.name, err)
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(filepath.Join(repo, "a.txt"), later.Add(time.Minute), later.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}

			code, out, errOut := runIndexCmd("--scip", fixtureFile(t), "--repo", repo)
			if code != 0 || !strings.HasPrefix(out, "indexed ") {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Errorf("adamap index ran the program the repository names in %s", c.name)
			}
		})
	}
}

// In a partial clone, git status fetches a missing HEAD tree from the promisor remote, and that runs
// the remote's uploadpack program from the repository's config. No -c option turns the fetch off:
// GIT_NO_LAZY_FETCH does.
func TestIndexDoesNotLazyFetchFromThePromisorRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	git(t, repo, "init", "-q")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-q", "-m", "one")
	tree := git(t, repo, "rev-parse", "HEAD^{tree}")
	git(t, repo, "config", "extensions.partialClone", "origin")
	git(t, repo, "config", "remote.origin.promisor", "true")
	git(t, repo, "config", "remote.origin.url", filepath.ToSlash(t.TempDir()))
	git(t, repo, "config", "remote.origin.uploadpack", "echo x > '"+filepath.ToSlash(marker)+"'; false")
	obj := filepath.Join(repo, ".git", "objects", tree[:2], tree[2:])
	if err := os.Remove(obj); err != nil {
		t.Fatalf("cannot make HEAD's tree missing: %v", err)
	}

	exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no").Run() // may fail; only the marker matters
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("a plain git status does not lazily fetch here (%v), so there is nothing to guard against", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	runIndexCmd("--scip", fixtureFile(t), "--repo", repo) // its exit code is not the point
	if _, err := os.Stat(marker); err == nil {
		t.Error("adamap index ran the uploadpack program the repository names for its promisor remote")
	}
}
