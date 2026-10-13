package main

import (
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func TestCallersPrintsEachCallerWithTheLinesItReferencesFrom(t *testing.T) {
	repo := queryRepo(t)
	want := "calls/a.go:8 Function B refs=10\ncalls/b.go:8 Function Rec refs=11\ncalls/b.go:19 Method A refs=20\n"
	if got := mustRun(t, "callers", "C", "--repo", repo); got != want {
		t.Errorf("callers of C:\n got %q\nwant %q", got, want)
	}
	// Rec calls itself; Thing calls it past the end of its method, so the method is not the caller.
	want = "calls/b.go:2 Function C refs=4\ncalls/b.go:8 Function Rec refs=10\ncalls/b.go:17 Struct Thing refs=22\n"
	if got := mustRun(t, "callers", "Rec", "--repo", repo); got != want {
		t.Errorf("callers of Rec:\n got %q\nwant %q", got, want)
	}
}

func TestCallersSaysHowManyReferencesLieOutsideEveryDefinition(t *testing.T) {
	code, out, errOut := runCmd("callers", sciptest.FnA, "--repo", queryRepo(t))
	if code != 0 || out != "calls/a.go:14 Function D refs=15\n" || !strings.Contains(errOut, "1 reference") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestCallersOfASymbolNobodyCallsSaysSo(t *testing.T) {
	code, out, errOut := runCmd("callers", "D", "--repo", queryRepo(t))
	if code != 0 || out != "" || !strings.Contains(errOut, "no callers") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestCallersRefusesAnAmbiguousNameAndAnUnknownOne(t *testing.T) {
	repo := queryRepo(t)
	if code, _, errOut := runCmd("callers", "A", "--repo", repo); code != 1 || !strings.Contains(errOut, sciptest.ThingA) {
		t.Errorf("ambiguous: exit %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCmd("callers", "Nope", "--repo", repo); code != 1 || !strings.Contains(errOut, `"Nope"`) {
		t.Errorf("unknown: exit %d, stderr %q", code, errOut)
	}
}

func TestCallersJSONHasTheDocumentedShape(t *testing.T) {
	repo := queryRepo(t)
	m := decodeJSON(t, mustRun(t, "callers", "B", "--repo", repo, "--json"))
	if _, ok := m["unattributed"]; ok || len(m) != 3 {
		t.Errorf("keys = %v: unattributed is left out when it is 0", m)
	}
	callers := m["callers"].([]any)
	c := callers[0].(map[string]any)
	if c["symbol"].(map[string]any)["id"] != sciptest.FnA {
		t.Errorf("caller = %v", c)
	}
	site := c["sites"].([]any)[0].(map[string]any)
	if site["line"] != 4.0 || site["col"] != 2.0 || site["kind"] != "reference" {
		t.Errorf("site = %v", site)
	}
	m = decodeJSON(t, mustRun(t, "callers", sciptest.FnA, "--repo", repo, "--json"))
	if m["unattributed"] != 1.0 {
		t.Errorf("unattributed = %v, want 1", m["unattributed"])
	}
	m = decodeJSON(t, mustRun(t, "callers", "D", "--repo", repo, "--json"))
	if cs, ok := m["callers"].([]any); !ok || len(cs) != 0 {
		t.Errorf("callers = %#v, want an empty list", m["callers"])
	}
}
