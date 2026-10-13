package query

import (
	"errors"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

func TestSafeLeavesPrintableTextAloneAndQuotesTheRest(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"Handle", "Handle"},
		{"pkg/d001.go", "pkg/d001.go"},
		{"naïve π 名前", "naïve π 名前"},
		{"a b", "a b"},
		{"x\x1b]52;c;ZXZpbA==\x07", `"x\x1b]52;c;ZXZpbA==\a"`},
		{"line\nbreak", `"line\nbreak"`},
		{"tab\there", `"tab\there"`},
		{"evil\u202egpj.exe", `"evil\u202egpj.exe"`},
		{"zero\u200bwidth", `"zero\u200bwidth"`},
		{"bad\xffbyte", `"bad\xffbyte"`},
	} {
		if got := Safe(tc.in); got != tc.want {
			t.Errorf("Safe(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// hostile is an index whose names carry a raw ESC and a bidi override.
func hostile() *Index {
	return New(&model.Index{Symbols: []model.Symbol{
		{ID: "s m p v esc#", DisplayName: "a\x1b[2Jb", Kind: "Fun\x1bction",
			Definition: &model.Location{Path: "dir\x1b/f.go", Range: model.Range{StartLine: 1}}},
		{ID: "s m p v bidi#", DisplayName: "a\u202eb"},
	}})
}

func TestShortQuotesWhatCouldDriveATerminal(t *testing.T) {
	got := hostile().Find("a", 0).Symbols
	if len(got) != 2 {
		t.Fatalf("Find returned %d symbols, want 2", len(got))
	}
	for _, s := range got {
		line := s.Short()
		if strings.ContainsAny(line, "\x1b\u202e") {
			t.Errorf("Short() = %q holds a raw control or format character", line)
		}
	}
	if want := `"dir\x1b/f.go":2 "Fun\x1bction" "a\x1b[2Jb"`; got[1].Short() != want {
		t.Errorf("Short() = %s, want %s", got[1].Short(), want)
	}
	if got[1].Name != "a\x1b[2Jb" {
		t.Errorf("the Name field was changed to %q: JSON must keep the raw value", got[1].Name)
	}
}

func TestAmbiguousErrorQuotesNamesAndSymbolIDs(t *testing.T) {
	x := New(&model.Index{Symbols: []model.Symbol{
		{ID: "s m p v one\x1b#", DisplayName: "dup\u202e"},
		{ID: "s m p v two#", DisplayName: "dup\u202e"},
	}})
	_, err := x.Resolve("dup\u202e")
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want an AmbiguousError", err)
	}
	if strings.ContainsAny(err.Error(), "\x1b\u202e") {
		t.Errorf("message %q holds a raw control or format character", err.Error())
	}
}
