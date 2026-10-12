package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// fixtureMap is the map of the SCIP fixture: b.go references Add in a.go, so a.go ranks first, and
// Add, used from b.go, leads a.go. Adder#Add has no kind or display name, so its label is sym and
// its name comes from its symbol.
const fixtureMap = `pkg/a.go
  func  Add(a, b int) int
  interface  Adder
pkg/b.go
  sym  Add
  struct  impl
`

// indexedRepo is a repository whose store holds the SCIP fixture at testCommit.
func indexedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if code, _, errOut := runIndexCmd("--scip", fixtureFile(t), "--commit", testCommit, "--repo", repo); code != 0 {
		t.Fatalf("index: exit %d, stderr %q", code, errOut)
	}
	return repo
}

func runMapCmd(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(append([]string{"map"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestMapPrintsTheRankedFilesOfTheLatestStore(t *testing.T) {
	repo := indexedRepo(t)
	for _, args := range [][]string{{"--repo", repo}, {"--repo", repo, "--commit", testCommit}} {
		code, out, errOut := runMapCmd(args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if out != fixtureMap {
			t.Errorf("%v: stdout\n%s\nwant\n%s", args, out, fixtureMap)
		}
	}
}

func TestMapStaysWithinTheTokens(t *testing.T) {
	repo := indexedRepo(t)
	// The path and the first symbol are 9 + 26 bytes: 9 tokens.
	code, out, errOut := runMapCmd("--repo", repo, "--tokens", "9")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if want := "pkg/a.go\n  func  Add(a, b int) int\n"; out != want {
		t.Errorf("stdout %q, want %q", out, want)
	}
	code, out, errOut = runMapCmd("--repo", repo, "--tokens", "8")
	if code != 0 || out != "" || !strings.Contains(errOut, "nothing fits in 8 tokens") {
		t.Errorf("--tokens 8: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestMapSaysWhenForMatchesNothing(t *testing.T) {
	repo := indexedRepo(t)
	code, out, errOut := runMapCmd("--repo", repo, "--for", "Zebra")
	if code != 0 || out != fixtureMap {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if !strings.Contains(errOut, `nothing matches --for "Zebra"`) {
		t.Errorf("stderr %q does not say --for matched nothing", errOut)
	}
	// impl matches, so b.go gains, and nothing is said.
	code, _, errOut = runMapCmd("--repo", repo, "--for", "IMPL")
	if code != 0 || errOut != "" {
		t.Errorf("--for IMPL: exit %d, stderr %q", code, errOut)
	}
}

func TestMapJSONHasTheSameContent(t *testing.T) {
	repo := indexedRepo(t)
	code, out, errOut := runMapCmd("--repo", repo, "--json", "--for", "impl")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var got mapJSON
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not the map's JSON: %v\n%s", err, out)
	}
	if got.Commit != testCommit || got.Tokens != 1024 || got.Matched != 1 || len(got.For) != 1 || got.For[0] != "impl" {
		t.Errorf("header %+v", got)
	}
	// Rebuild the text from the JSON: it must be the text form, and its estimate.
	var text strings.Builder
	for _, f := range got.Files {
		text.WriteString(f.Path + "\n")
		for _, s := range f.Symbols {
			text.WriteString("  " + s.Kind + "  " + s.Detail + "\n")
		}
	}
	_, plain, _ := runMapCmd("--repo", repo, "--for", "impl")
	if text.String() != plain {
		t.Errorf("the JSON reads as\n%s\nthe text is\n%s", text.String(), plain)
	}
	if want := (len(plain) + 3) / 4; got.Estimate != want {
		t.Errorf("estimate %d, want %d", got.Estimate, want)
	}
	add := got.Files[0].Symbols[0]
	if got.Files[0].Path != "pkg/a.go" || add.Name != "Add" || add.Refs != 1 || add.ID == "" || add.Score <= 0 || got.Files[0].Rank <= 0 {
		t.Errorf("first file %+v", got.Files[0])
	}
}

func TestMapOfAnEmptyStore(t *testing.T) {
	repo := t.TempDir()
	if _, err := store.Write(repo, store.Header{Adamap: Version, Commit: testCommit, Created: time.Now().UTC()}, &model.Index{}); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runMapCmd("--repo", repo)
	if code != 0 || out != "" || !strings.Contains(errOut, "defines no symbols") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runMapCmd("--repo", repo, "--json")
	if code != 0 || !strings.Contains(out, `"files": []`) {
		t.Errorf("--json: exit %d, stdout %q, want an empty files list", code, out)
	}
}

func TestMapWithoutAStoreSaysToIndex(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"--repo", repo}, {"--repo", repo, "--commit", testCommit}} {
		code, out, errOut := runMapCmd(args...)
		if code != 1 || out != "" || !strings.Contains(errOut, "adamap index") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestMapUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--tokens", "0"},
		{"--tokens", "-5"},
		{"--tokens", "many"},
		{"--commit", "abc"},
		{"extra"},
		{"--bogus"},
	} {
		code, out, errOut := runMapCmd(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: adamap map") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	code, out, _ := runMapCmd("-h")
	if code != 0 || !strings.HasPrefix(out, "usage: adamap map") || !strings.Contains(out, "-tokens") {
		t.Errorf("-h: exit %d, stdout %q", code, out)
	}
}
