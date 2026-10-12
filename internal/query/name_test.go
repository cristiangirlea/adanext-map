package query

import (
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func TestNameFromSymbolReadsTheLastDescriptor(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{sciptest.FnA, "A"},
		{sciptest.Thing, "Thing"},
		{sciptest.ThingA, "A"},
		{sciptest.FnRemote, "Remote"},
		{sciptest.Fmt, "fmt"},
		{"rust-analyzer cargo foo 0.1.0 util/parse_args().", "parse_args"},
		{"scip-go gomod a/b v1 `x/y`/T#M(+1).", "M"},
		{"s m p v `odd``name`.", "odd`name"},
		{"s m p v T#[U]", "U"},
		{"s m p v f().(arg)", "arg"},
		{"scheme  with  spaces mgr pkg v1 Name#", "Name"},
		{"s m p v macro!", "macro"},
		{"s m p v meta:", "meta"},
	} {
		if got := nameFromSymbol(tc.id); got != tc.want {
			t.Errorf("nameFromSymbol(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestNameFromSymbolGivesNothingForWhatItCannotRead(t *testing.T) {
	for _, id := range []string{
		"", "local 0", "short", "s m p v", "s m p v ", "s m p v name", "s m p v `unclosed#",
		"s m p v f(", "s m p v f(+1)x", "s m p v [T", "s m p v ()", "s m p v a b#",
	} {
		if got := nameFromSymbol(id); got != "" {
			t.Errorf("nameFromSymbol(%q) = %q, want none", id, got)
		}
	}
}
