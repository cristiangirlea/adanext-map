package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func TestOutlinePrintsOneLinePerSymbol(t *testing.T) {
	repo := queryRepo(t)
	want := "calls/a.go:2 Function A\ncalls/a.go:8 Function B\ncalls/a.go:14 Function D\n"
	if got := mustRun(t, "outline", sciptest.PathA, "--repo", repo); got != want {
		t.Errorf("outline:\n got %q\nwant %q", got, want)
	}
	want = "calls/b.go:2 Function C\ncalls/b.go:8 Function Rec\ncalls/b.go:17 Struct Thing\ncalls/b.go:19 Method A\n"
	if got := mustRun(t, "outline", sciptest.PathB, "--repo", repo); got != want {
		t.Errorf("outline:\n got %q\nwant %q", got, want)
	}
}

func TestOutlineMatchesThePathInAnySpellingRelativeToTheRepo(t *testing.T) {
	repo := queryRepo(t)
	want := mustRun(t, "outline", sciptest.PathA, "--repo", repo)
	for _, arg := range []string{
		filepath.Join("calls", "a.go"), // backslashes on Windows
		"./calls/a.go",
		filepath.Join(repo, "calls", "a.go"),
	} {
		if got := mustRun(t, "outline", arg, "--repo", repo); got != want {
			t.Errorf("outline %q: %q, want %q", arg, got, want)
		}
	}
}

func TestOutlineDefaultsTheRepoToTheWorkingDirectory(t *testing.T) {
	repo := queryRepo(t)
	t.Chdir(repo)
	if got := mustRun(t, "outline", sciptest.PathA); !strings.HasPrefix(got, "calls/a.go:2 Function A\n") {
		t.Errorf("outline: %q", got)
	}
}

func TestOutlineOfAFileTheIndexDoesNotHaveFails(t *testing.T) {
	repo := queryRepo(t)
	code, out, errOut := runCmd("outline", "calls/missing.go", "--repo", repo)
	if code != 1 || out != "" || !strings.Contains(errOut, `no document "calls/missing.go"`) {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	// A file outside the repository can never be in its index.
	code, _, errOut = runCmd("outline", filepath.Join(t.TempDir(), "x.go"), "--repo", repo)
	if code != 1 || !strings.Contains(errOut, "outside") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestOutlineOfAFileWithoutSymbolsIsEmptyAndSaysSo(t *testing.T) {
	repo := queryRepo(t)
	code, out, errOut := runCmd("outline", sciptest.PathEmpty, "--repo", repo)
	if code != 0 || out != "" || !strings.Contains(errOut, "no symbols") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	want := `{"commit":"` + testCommit + `","path":"calls/empty.go","symbols":[]}` + "\n"
	if got := mustRun(t, "outline", sciptest.PathEmpty, "--repo", repo, "--json"); got != want {
		t.Errorf("--json:\n got %q\nwant %q", got, want)
	}
}

func TestOutlineJSONHasTheDocumentedShape(t *testing.T) {
	repo := queryRepo(t)
	m := decodeJSON(t, mustRun(t, "outline", sciptest.PathA, "--repo", repo, "--json"))
	if m["path"] != sciptest.PathA {
		t.Errorf("path = %v", m["path"])
	}
	syms := m["symbols"].([]any)
	if len(syms) != 3 {
		t.Fatalf("symbols = %v", syms)
	}
	first := syms[0].(map[string]any)
	want := map[string]any{
		"id": sciptest.FnA, "name": "A", "kind": "Function", "path": sciptest.PathA,
		"line": 2.0, "end_line": 6.0, "signature": "func A()",
	}
	if len(first) != len(want) {
		t.Errorf("first symbol has fields %v, want %v", first, want)
	}
	for k, v := range want {
		if first[k] != v {
			t.Errorf("%s = %v, want %v", k, first[k], v)
		}
	}
	if len(m) != 3 {
		t.Errorf("top-level keys %v, want commit, path and symbols", m)
	}
}
