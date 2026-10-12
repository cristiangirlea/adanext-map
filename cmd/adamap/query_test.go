package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/cristiangirlea/adanext-map/internal/scip"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// otherCommit is a second commit, indexed after testCommit, so it is the latest.
const otherCommit = "fedcba9876543210fedcba9876543210fedcba98"

// indexInto indexes x as commit into repo.
func indexInto(t *testing.T, repo, commit string, x *scippb.Index) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.scip")
	writeFile(t, path, string(sciptest.Bytes(t, x)))
	if code, _, errOut := runIndexCmd("--scip", path, "--commit", commit, "--repo", repo); code != 0 {
		t.Fatalf("index: exit %d, stderr %q", code, errOut)
	}
}

// queryRepo is a repository whose store holds the call-graph fixture at testCommit.
func queryRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	indexInto(t, repo, testCommit, sciptest.CallGraph())
	return repo
}

// runCmd runs one adamap command line.
func runCmd(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// mustRun runs the command and fails the test unless it exits 0 with nothing on stderr.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errOut := runCmd(args...)
	if code != 0 || errOut != "" {
		t.Fatalf("adamap %s: exit %d, stderr %q", strings.Join(args, " "), code, errOut)
	}
	return out
}

// decodeJSON parses one JSON object from out, as a generic map.
func decodeJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("--json should print one line, got %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return m
}

var queryCommands = []struct{ name, arg string }{
	{"outline", sciptest.PathA},
	{"find", "A"},
	{"refs", "Rec"},
}

func TestQueryCommandsSayToRunIndexWhenThereIsNoStore(t *testing.T) {
	for _, c := range queryCommands {
		for _, extra := range [][]string{nil, {"--commit", testCommit}} {
			args := append([]string{c.name, c.arg, "--repo", t.TempDir()}, extra...)
			code, out, errOut := runCmd(args...)
			if code != 1 || out != "" || !strings.Contains(errOut, "adamap index") || !strings.HasPrefix(errOut, "adamap "+c.name+": ") {
				t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
			}
		}
	}
}

func TestQueryCommandsReadTheLatestCommitUnlessToldOtherwise(t *testing.T) {
	repo := queryRepo(t)
	indexInto(t, repo, otherCommit, sciptest.Index()) // the latest now; has pkg/a.go, not calls/a.go
	if latest, _ := store.Latest(repo); latest != otherCommit {
		t.Fatalf("latest = %q", latest)
	}
	code, _, errOut := runCmd("outline", sciptest.PathA, "--repo", repo)
	if code != 1 || !strings.Contains(errOut, "no document") || !strings.Contains(errOut, otherCommit[:12]) {
		t.Errorf("latest: exit %d, stderr %q", code, errOut)
	}
	out := mustRun(t, "outline", sciptest.PathA, "--repo", repo, "--commit", testCommit)
	if !strings.HasPrefix(out, "calls/a.go:2 Function A\n") {
		t.Errorf("--commit: stdout %q", out)
	}
	out = mustRun(t, "outline", "pkg/a.go", "--repo", repo)
	if !strings.Contains(out, "pkg/a.go:") {
		t.Errorf("latest: stdout %q", out)
	}
}

func TestQueryCommandsNameTheCommitInJSON(t *testing.T) {
	repo := queryRepo(t)
	for _, c := range queryCommands {
		m := decodeJSON(t, mustRun(t, c.name, c.arg, "--repo", repo, "--json"))
		if m["commit"] != testCommit {
			t.Errorf("%s: commit = %v", c.name, m["commit"])
		}
		if _, ok := m["dirty"]; ok {
			t.Errorf("%s: dirty should be left out of a clean store", c.name)
		}
	}
}

func TestQueryCommandsReportADirtyStoreInJSON(t *testing.T) {
	repo := t.TempDir()
	// A dirty header is written by `adamap index` in a git repository with changes; write one directly.
	idx, err := scip.Convert(sciptest.CallGraph())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(repo, store.Header{Commit: testCommit, Dirty: true, Adamap: Version}, idx); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, mustRun(t, "outline", sciptest.PathA, "--repo", repo, "--json"))
	if m["dirty"] != true {
		t.Errorf("dirty = %v", m["dirty"])
	}
}

func TestQueryCommandsRejectABadCommitFlag(t *testing.T) {
	for _, c := range queryCommands {
		code, _, errOut := runCmd(c.name, c.arg, "--commit", "abc123", "--repo", queryRepo(t))
		if code != 2 || !strings.Contains(errOut, "--commit") {
			t.Errorf("%s: exit %d, stderr %q", c.name, code, errOut)
		}
	}
}

func TestQueryCommandsWantExactlyOneArgument(t *testing.T) {
	for _, c := range queryCommands {
		for _, args := range [][]string{{c.name}, {c.name, c.arg, "extra"}} {
			code, out, errOut := runCmd(args...)
			if code != 2 || out != "" || !strings.Contains(errOut, "usage: adamap "+c.name) {
				t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
			}
		}
		if code, _, errOut := runCmd(c.name, c.arg, "--no-such-flag"); code != 2 || !strings.Contains(errOut, "no-such-flag") {
			t.Errorf("%s: unknown flag: exit %d, stderr %q", c.name, code, errOut)
		}
	}
}

func TestQueryCommandsPrintTheirFlagsForHelp(t *testing.T) {
	for _, c := range queryCommands {
		code, out, errOut := runCmd(c.name, "-h")
		if code != 0 || errOut != "" || !strings.HasPrefix(out, "usage: adamap "+c.name) {
			t.Errorf("%s -h: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
		for _, flag := range []string{"-repo", "-commit", "-json"} {
			if !strings.Contains(out, flag) {
				t.Errorf("%s -h does not mention %s", c.name, flag)
			}
		}
	}
}

func TestQueryCommandsTakeFlagsAfterTheArgument(t *testing.T) {
	repo := queryRepo(t)
	// Two symbols are called A, so a limit of 1 cuts the list (and says so on stderr).
	_, before, _ := runCmd("find", "--repo", repo, "--limit", "1", "A")
	_, after, _ := runCmd("find", "A", "--repo", repo, "--limit", "1")
	if before != after || strings.Count(before, "\n") != 1 {
		t.Errorf("flags before %q, after %q", before, after)
	}
	// After --, everything is the argument, even a leading dash.
	code, _, errOut := runCmd("refs", "--repo", repo, "--", "-x")
	if code != 1 || !strings.Contains(errOut, `"-x"`) {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestHelpListsTheQueryCommands(t *testing.T) {
	_, out, _ := runCmd("help")
	for _, c := range queryCommands {
		if !strings.Contains(out, "  "+c.name+" ") {
			t.Errorf("help does not list %s:\n%s", c.name, out)
		}
	}
}

func TestIndexPathMatchesTheIndexAfterSlashingAndMakingItRelative(t *testing.T) {
	repo := t.TempDir()
	for _, tc := range []struct {
		arg, want string
	}{
		{"calls/a.go", "calls/a.go"},
		{filepath.Join("calls", "a.go"), "calls/a.go"},
		{"./calls/a.go", "calls/a.go"},
		{"calls//sub/../a.go", "calls/a.go"},
		{filepath.Join(repo, "calls", "a.go"), "calls/a.go"},
		{filepath.Join(repo, "calls") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "calls" + string(os.PathSeparator) + "a.go", "calls/a.go"},
	} {
		got, err := indexPath(repo, tc.arg)
		if err != nil || got != tc.want {
			t.Errorf("indexPath(%q) = %q, %v; want %q", tc.arg, got, err, tc.want)
		}
	}
}

func TestIndexPathRefusesAnAbsolutePathOutsideTheRepository(t *testing.T) {
	repo := t.TempDir()
	other := filepath.Join(t.TempDir(), "calls", "a.go")
	if got, err := indexPath(repo, other); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("indexPath(%q) = %q, %v; want an error that says outside", other, got, err)
	}
}
