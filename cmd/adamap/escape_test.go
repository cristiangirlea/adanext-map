package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// hostileRepo holds a store whose names and kind carry a raw ESC and a bidi override and whose path a
// zero-width space (a control character in a path would not load), as an honest index of a hostile
// repository (or a committed .adamap) could.
func hostileRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	def := model.Range{StartLine: 3, StartCol: 5, EndLine: 3, EndCol: 9}
	idx := &model.Index{
		Documents: []model.Document{{Path: "d\u202e/f.go", Occurrences: []model.Occurrence{
			{Range: def, Symbol: "s m p v esc().", Roles: model.RoleDefinition},
			{Range: model.Range{StartLine: 8, EndLine: 8, EndCol: 4}, Symbol: "s m p v esc().", Roles: model.RoleReference},
			{Range: model.Range{StartLine: 9, EndLine: 9, EndCol: 4}, Symbol: "s m p v bidi#", Roles: model.RoleDefinition},
			{Range: model.Range{StartLine: 10, EndLine: 10, EndCol: 4}, Symbol: "s m p v bidi2#", Roles: model.RoleDefinition},
		}}},
		Symbols: []model.Symbol{
			{ID: "s m p v bidi#", DisplayName: "dup\u202e"},
			{ID: "s m p v bidi2#", DisplayName: "dup\u202e"},
			{ID: "s m p v esc().", DisplayName: "ev\x1b[2Jil", Kind: "Fun\x1bction",
				Definition: &model.Location{Path: "d\u202e/f.go", Range: def}},
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
		{"outline", "d\u202e/f.go"},
		{"impact", "s m p v esc()."},
	} {
		code, out, errOut := runCmd(append(args, "--repo", repo)...)
		if code != 0 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
		noRaw(t, args[0]+" stdout", out)
		noRaw(t, args[0]+" stderr", errOut)
	}
	if got := mustRun(t, "find", "ev", "--repo", repo); got != `"d\u202e/f.go":4 "Fun\x1bction" "ev\x1b[2Jil"`+"\n" {
		t.Errorf("find: %q", got)
	}
	if got := mustRun(t, "refs", "s m p v esc().", "--repo", repo); got != `"d\u202e/f.go":4 definition "ev\x1b[2Jil"`+"\n"+
		`"d\u202e/f.go":9 reference "ev\x1b[2Jil"`+"\n" {
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
	// encoding/json escapes ESC itself but leaves U+202E as it is; decoding gives the stored values back.
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("JSON holds a raw control character: %q", out)
	}
	m := decodeJSON(t, out)
	sym := m["symbols"].([]any)[0].(map[string]any)
	if sym["name"] != "ev\x1b[2Jil" {
		t.Errorf("name = %q, want the raw stored value", sym["name"])
	}
	if sym["path"] != "d\u202e/f.go" {
		t.Errorf("path = %q, want the raw stored value", sym["path"])
	}
}

// rawControl reports whether s holds a DEL or a C1 control (U+0080 to U+009F) as it is written: a
// terminal may act on them, and encoding/json leaves them as they are.
func rawControl(s string) bool {
	return strings.Contains(s, "\x7f") || strings.ContainsAny(s, "\u0080\u0085\u008d\u009b\u009d\u009f")
}

func TestJSONOutputEscapesDELAndTheC1Controls(t *testing.T) {
	repo := t.TempDir()
	def := model.Range{StartLine: 3, StartCol: 5, EndLine: 3, EndCol: 9}
	const id = "s m p v ctl\x7f()."
	idx := &model.Index{
		Documents: []model.Document{{Path: "pkg/f.go", Occurrences: []model.Occurrence{
			{Range: def, Symbol: id, Roles: model.RoleDefinition},
		}}},
		Symbols: []model.Symbol{{ID: id, DisplayName: "de\x7fl\u009b[2Jx\u0085", Kind: "Function",
			Definition: &model.Location{Path: "pkg/f.go", Range: def}}},
	}
	if _, err := store.Write(repo, store.Header{Commit: testCommit}, idx); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"find", "de", "--json"}, {"refs", id, "--json"}, {"map", "--json"}} {
		out := mustRun(t, append(args, "--repo", repo)...)
		if rawControl(out) {
			t.Errorf("%v: the JSON holds a raw DEL or C1 control: %q", args, out)
		}
		if !strings.Contains(out, `\u007f`) || !strings.Contains(out, `\u009b`) && args[0] != "refs" {
			t.Errorf("%v: the controls should be written as unicode escapes: %q", args, out)
		}
	}
	// Decoding gives the stored value back.
	sym := decodeJSON(t, mustRun(t, "find", "de", "--json", "--repo", repo))["symbols"].([]any)[0].(map[string]any)
	if sym["name"] != "de\x7fl\u009b[2Jx\u0085" || sym["id"] != id {
		t.Errorf("name %q, id %q: want the raw stored values", sym["name"], sym["id"])
	}
}

func TestWriteJSONEscapesOnlyDELAndTheC1Controls(t *testing.T) {
	in := map[string]string{"s": "\x7f\xc2\x80\xc2\x9f|\xc2\xa0|\xc3\x82|\xc3\xa9|\xe2\x80\xa8|\xe2\x80\xae|\x1b|<>&"}
	var b bytes.Buffer
	if err := writeJSON(&b, in, func(e *json.Encoder) { e.SetEscapeHTML(false) }); err != nil {
		t.Fatal(err)
	}
	want := `{"s":"\u007f\u0080\u009f|` + "\xc2\xa0|\xc3\x82|\xc3\xa9|\x5cu2028|\xe2\x80\xae" + `|\u001b|<>&"}` + "\n"
	if b.String() != want {
		t.Errorf("got  %q\nwant %q", b.String(), want)
	}
	var back map[string]string
	if err := json.Unmarshal(b.Bytes(), &back); err != nil || back["s"] != in["s"] {
		t.Errorf("decoding gives %q, %v: want the input back", back["s"], err)
	}
}
