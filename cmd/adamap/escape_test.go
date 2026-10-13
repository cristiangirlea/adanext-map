package main

import (
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// hostileRepo holds a store whose names, kind and path carry a raw ESC and a bidi override, as an
// honest index of a hostile repository (or a committed .adamap) could.
func hostileRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	def := model.Range{StartLine: 3, StartCol: 5, EndLine: 3, EndCol: 9}
	idx := &model.Index{
		Documents: []model.Document{{Path: "d\x1b]0;x\x07/f.go", Occurrences: []model.Occurrence{
			{Range: def, Symbol: "s m p v esc().", Roles: model.RoleDefinition},
			{Range: model.Range{StartLine: 8, EndLine: 8, EndCol: 4}, Symbol: "s m p v esc().", Roles: model.RoleReference},
			{Range: model.Range{StartLine: 9, EndLine: 9, EndCol: 4}, Symbol: "s m p v bidi#", Roles: model.RoleDefinition},
			{Range: model.Range{StartLine: 10, EndLine: 10, EndCol: 4}, Symbol: "s m p v bidi2#", Roles: model.RoleDefinition},
		}}},
		Symbols: []model.Symbol{
			{ID: "s m p v bidi#", DisplayName: "dup\u202e"},
			{ID: "s m p v bidi2#", DisplayName: "dup\u202e"},
			{ID: "s m p v esc().", DisplayName: "ev\x1b[2Jil", Kind: "Fun\x1bction",
				Definition: &model.Location{Path: "d\x1b]0;x\x07/f.go", Range: def}},
		},
	}
	if _, err := store.Write(repo, store.Header{Commit: testCommit}, idx); err != nil {
		t.Fatal(err)
	}
	return repo
}

func noRaw(t *testing.T, what, s string) {
	t.Helper()
	if strings.ContainsAny(s, "\x1b\x07\u202e") {
		t.Errorf("%s holds a raw control or format character: %q", what, s)
	}
}

func TestTextOutputQuotesStoreStringsThatCouldDriveATerminal(t *testing.T) {
	repo := hostileRepo(t)
	for _, args := range [][]string{
		{"find", "ev"},
		{"refs", "s m p v esc()."},
		{"callers", "s m p v esc()."},
		{"outline", "d\x1b]0;x\x07/f.go"},
		{"impact", "s m p v esc()."},
	} {
		code, out, errOut := runCmd(append(args, "--repo", repo)...)
		if code != 0 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
		noRaw(t, args[0]+" stdout", out)
		noRaw(t, args[0]+" stderr", errOut)
	}
	if got := mustRun(t, "find", "ev", "--repo", repo); got != `"d\x1b]0;x\a/f.go":4 "Fun\x1bction" "ev\x1b[2Jil"`+"\n" {
		t.Errorf("find: %q", got)
	}
	if got := mustRun(t, "refs", "s m p v esc().", "--repo", repo); got != `"d\x1b]0;x\a/f.go":4 definition "ev\x1b[2Jil"`+"\n"+
		`"d\x1b]0;x\a/f.go":9 reference "ev\x1b[2Jil"`+"\n" {
		t.Errorf("refs: %q", got)
	}
}

func TestAmbiguousNameErrorQuotesTheCandidates(t *testing.T) {
	code, _, errOut := runCmd("refs", "dup\u202e", "--repo", hostileRepo(t))
	if code != 1 || !strings.Contains(errOut, "fits 2 symbols") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	noRaw(t, "the ambiguity error", errOut)
}

func TestJSONOutputKeepsTheRawValues(t *testing.T) {
	out := mustRun(t, "find", "ev", "--repo", hostileRepo(t), "--json")
	// encoding/json escapes ESC itself, and decoding gives the stored name back.
	noRaw(t, "JSON", out)
	m := decodeJSON(t, out)
	syms := m["symbols"].([]any)
	if name := syms[0].(map[string]any)["name"]; name != "ev\x1b[2Jil" {
		t.Errorf("name = %q, want the raw stored value", name)
	}
}
