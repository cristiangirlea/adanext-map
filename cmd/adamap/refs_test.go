package main

import (
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func TestRefsPrintsDefinitionsFirstGroupedByFile(t *testing.T) {
	repo := queryRepo(t)
	want := "calls/b.go:2 definition C\ncalls/b.go:11 reference C\ncalls/b.go:20 reference C\ncalls/a.go:10 reference C\n"
	// By display name, ignoring case, and by symbol string.
	for _, spec := range []string{"C", "c", sciptest.FnC} {
		if got := mustRun(t, "refs", spec, "--repo", repo); got != want {
			t.Errorf("refs %q:\n got %q\nwant %q", spec, got, want)
		}
	}
}

func TestRefsOfASymbolOnlyReferencedWorksByTheNameInItsSymbolString(t *testing.T) {
	repo := queryRepo(t)
	want := "calls/a.go:11 reference Remote\n"
	for _, spec := range []string{"Remote", sciptest.FnRemote} {
		if got := mustRun(t, "refs", spec, "--repo", repo); got != want {
			t.Errorf("refs %q: %q, want %q", spec, got, want)
		}
	}
}

func TestRefsRefusesAnAmbiguousNameAndListsTheCandidates(t *testing.T) {
	repo := queryRepo(t)
	code, out, errOut := runCmd("refs", "A", "--repo", repo)
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	for _, want := range []string{"adamap refs:", `"A"`, "calls/a.go:2 Function A", "calls/b.go:19 Method A", sciptest.FnA, sciptest.ThingA} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
	// Either candidate's symbol string works.
	if got := mustRun(t, "refs", sciptest.ThingA, "--repo", repo); got != "calls/b.go:19 definition A\n" {
		t.Errorf("refs of the method = %q", got)
	}
}

func TestRefsOfAnUnknownSymbolFailsAndPointsToFind(t *testing.T) {
	code, out, errOut := runCmd("refs", "Nope", "--repo", queryRepo(t))
	if code != 1 || out != "" || !strings.Contains(errOut, `"Nope"`) || !strings.Contains(errOut, "adamap find") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestRefsJSONHasTheDocumentedShape(t *testing.T) {
	repo := queryRepo(t)
	want := `{"commit":"` + testCommit + `","symbol":{"id":"` + sciptest.FnRemote + `","name":"Remote"},"total":1,` +
		`"files":[{"path":"calls/a.go","refs":[{"line":11,"col":2,"kind":"reference"}]}]}` + "\n"
	if got := mustRun(t, "refs", "Remote", "--repo", repo, "--json"); got != want {
		t.Errorf("--json:\n got %q\nwant %q", got, want)
	}
	m := decodeJSON(t, mustRun(t, "refs", "C", "--repo", repo, "--json"))
	files := m["files"].([]any)
	if m["total"] != 4.0 || len(files) != 2 || files[0].(map[string]any)["path"] != sciptest.PathB {
		t.Errorf("refs of C = %v", m)
	}
}
