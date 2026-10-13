package main

import (
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func TestFindPrintsMatchesBestFirstOneLineEach(t *testing.T) {
	repo := queryRepo(t)
	if got, want := mustRun(t, "find", "A", "--repo", repo), "calls/a.go:2 Function A\ncalls/b.go:19 Method A\n"; got != want {
		t.Errorf("find A:\n got %q\nwant %q", got, want)
	}
	// A prefix: Rec and Remote. Remote is never defined, so it has neither place nor kind.
	if got, want := mustRun(t, "find", "re", "--repo", repo), "calls/b.go:8 Function Rec\n- - Remote\n"; got != want {
		t.Errorf("find re:\n got %q\nwant %q", got, want)
	}
}

func TestFindLimitCutsTheListAndSaysHowMuchWasLeft(t *testing.T) {
	repo := queryRepo(t)
	code, out, errOut := runCmd("find", "A", "--limit", "1", "--repo", repo)
	if code != 0 || out != "calls/a.go:2 Function A\n" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
	if !strings.Contains(errOut, "1 more") || !strings.Contains(errOut, "--limit") {
		t.Errorf("stderr %q should say what was left out", errOut)
	}
}

func TestFindWithNoMatchIsNotAnError(t *testing.T) {
	repo := queryRepo(t)
	code, out, errOut := runCmd("find", "zzz", "--repo", repo)
	if code != 0 || out != "" || !strings.Contains(errOut, `no symbol matches "zzz"`) {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	want := `{"commit":"` + testCommit + `","query":"zzz","total":0,"symbols":[]}` + "\n"
	if got := mustRun(t, "find", "zzz", "--repo", repo, "--json"); got != want {
		t.Errorf("--json:\n got %q\nwant %q", got, want)
	}
}

func TestFindRejectsALimitThatIsNotPositive(t *testing.T) {
	for _, limit := range []string{"0", "-3", "x"} {
		code, _, errOut := runCmd("find", "A", "--limit", limit, "--repo", queryRepo(t))
		if code != 2 || !strings.Contains(errOut, "limit") {
			t.Errorf("--limit %s: exit %d, stderr %q", limit, code, errOut)
		}
	}
}

func TestFindJSONHasTheDocumentedShape(t *testing.T) {
	repo := queryRepo(t)
	want := `{"commit":"` + testCommit + `","query":"Remote","total":1,"symbols":[{"id":"` + sciptest.FnRemote +
		`","name":"Remote"}]}` + "\n"
	if got := mustRun(t, "find", "Remote", "--repo", repo, "--json"); got != want {
		t.Errorf("--json:\n got %q\nwant %q", got, want)
	}
	m := decodeJSON(t, mustRun(t, "find", "A", "--limit", "1", "--repo", repo, "--json"))
	if m["total"] != 2.0 || len(m["symbols"].([]any)) != 1 {
		t.Errorf("total %v with %d symbols: the limit cuts the list, not the total", m["total"], len(m["symbols"].([]any)))
	}
}
