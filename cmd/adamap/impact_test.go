package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func TestImpactOfASymbolListsWhatDependsOnItWithTheDepth(t *testing.T) {
	repo := queryRepo(t)
	want := "calls/a.go:8 Function B depth=1\ncalls/b.go:8 Function Rec depth=1\ncalls/b.go:19 Method A depth=1\n" +
		"calls/a.go:2 Function A depth=2\ncalls/b.go:17 Struct Thing depth=2\ncalls/a.go:14 Function D depth=3\n"
	if got := mustRun(t, "impact", "C", "--repo", repo); got != want {
		t.Errorf("impact of C:\n got %q\nwant %q", got, want)
	}
}

func TestImpactDepthLimitsTheWalk(t *testing.T) {
	repo := queryRepo(t)
	if got := mustRun(t, "impact", "C", "--depth", "1", "--repo", repo); strings.Count(got, "\n") != 3 || strings.Contains(got, "depth=2") {
		t.Errorf("--depth 1: %q", got)
	}
	if got := mustRun(t, "impact", "C", "--depth", "2", "--repo", repo); strings.Count(got, "\n") != 5 || strings.Contains(got, "depth=3") {
		t.Errorf("--depth 2: %q", got)
	}
}

func TestImpactRejectsADepthBelowOne(t *testing.T) {
	for _, depth := range []string{"0", "-1", "x"} {
		code, _, errOut := runCmd("impact", "C", "--depth", depth, "--repo", queryRepo(t))
		if code != 2 || !strings.Contains(errOut, "depth") {
			t.Errorf("--depth %s: exit %d, stderr %q", depth, code, errOut)
		}
	}
}

func TestImpactTerminatesOnACycle(t *testing.T) {
	// C and Rec call each other and Rec calls itself: a depth of 1000 is cut short by the cycle, not by the limit.
	repo := queryRepo(t)
	got := mustRun(t, "impact", "Rec", "--depth", "1000", "--repo", repo)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	seen := map[string]bool{}
	for _, l := range lines {
		place := l[:strings.Index(l, " depth=")]
		if seen[place] {
			t.Errorf("%q is listed twice", place)
		}
		seen[place] = true
	}
	if len(lines) != 6 || !strings.HasSuffix(lines[5], "Function D depth=4") {
		t.Errorf("impact of Rec = %q", got)
	}
}

func TestImpactOfAFileTakesAPathInAnySpelling(t *testing.T) {
	repo := queryRepo(t)
	want := "calls/a.go:8 Function B depth=1\ncalls/a.go:2 Function A depth=2\ncalls/a.go:14 Function D depth=3\n"
	for _, arg := range []string{sciptest.PathB, filepath.Join("calls", "b.go"), "./calls/b.go", filepath.Join(repo, "calls", "b.go")} {
		if got := mustRun(t, "impact", arg, "--repo", repo); got != want {
			t.Errorf("impact %q:\n got %q\nwant %q", arg, got, want)
		}
	}
	if got := mustRun(t, "impact", sciptest.PathB, "--depth", "1", "--repo", repo, "--json"); !strings.Contains(got, `"kind":"file"`) {
		t.Errorf("--json of a file: %q", got)
	}
}

func TestImpactSaysWhenNothingDependsOnTheTarget(t *testing.T) {
	repo := queryRepo(t)
	for _, arg := range []string{"D", sciptest.PathEmpty} {
		code, out, errOut := runCmd("impact", arg, "--repo", repo)
		if code != 0 || out != "" || !strings.Contains(errOut, "nothing depends on") {
			t.Errorf("impact %q: exit %d, stdout %q, stderr %q", arg, code, out, errOut)
		}
	}
}

func TestImpactRefusesAnAmbiguousNameAndFailsOnAnUnknownTarget(t *testing.T) {
	repo := queryRepo(t)
	if code, out, errOut := runCmd("impact", "A", "--repo", repo); code != 1 || out != "" || !strings.Contains(errOut, sciptest.ThingA) {
		t.Errorf("ambiguous: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, _, errOut := runCmd("impact", "Nope", "--repo", repo); code != 1 || !strings.Contains(errOut, `"Nope"`) {
		t.Errorf("unknown: exit %d, stderr %q", code, errOut)
	}
}

func TestImpactJSONHasTheDocumentedShape(t *testing.T) {
	repo := queryRepo(t)
	m := decodeJSON(t, mustRun(t, "impact", "C", "--repo", repo, "--json"))
	if m["target"] != sciptest.FnC || m["kind"] != "symbol" || m["depth"] != 3.0 || len(m) != 5 {
		t.Errorf("header = %v", m)
	}
	items := m["impacted"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 6 || first["depth"] != 1.0 || first["via"] != sciptest.FnC ||
		first["symbol"].(map[string]any)["id"] != sciptest.FnB {
		t.Errorf("impacted = %v", items)
	}
	m = decodeJSON(t, mustRun(t, "impact", "D", "--repo", repo, "--json"))
	if is, ok := m["impacted"].([]any); !ok || len(is) != 0 {
		t.Errorf("impacted = %#v, want an empty list", m["impacted"])
	}
}

func TestImpactOfAPathOutsideTheRepositorySaysSoAndNotThatNoSymbolFits(t *testing.T) {
	repo := queryRepo(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	code, out, errOut := runCmd("impact", outside, "--repo", repo)
	if code != 1 || out != "" || !strings.Contains(errOut, "outside the repository") || strings.Contains(errOut, "no such symbol") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}
