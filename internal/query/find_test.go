package query

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

// named builds an index whose global symbols have the given display names, in the order given.
// The IDs ascend in that order, so the order is also the store's.
func named(display ...string) *Index {
	var syms []model.Symbol
	for i, n := range display {
		syms = append(syms, model.Symbol{ID: fmt.Sprintf("s m p v id%03d#", i), DisplayName: n})
	}
	return New(&model.Index{Symbols: syms})
}

func TestFindRanksExactThenCaseInsensitiveThenPrefixThenSubstring(t *testing.T) {
	// Listed in the order the store would hold them (by ID), mixed across the groups.
	x := named("xPARSEx", "parse", "ParseFile", "PARSE", "Parse", "Reparse", "parser", "Other")
	got := x.Find("Parse", 0)
	want := []string{"Parse", "parse", "PARSE", "ParseFile", "parser", "xPARSEx", "Reparse"}
	// Within a group the order is the store's: parse before PARSE, ParseFile before parser, and so on.
	if !slices.Equal(names(got.Symbols), want) {
		t.Errorf("Find(Parse) = %v, want %v", names(got.Symbols), want)
	}
	if got.Query != "Parse" || got.Total != 7 {
		t.Errorf("query %q total %d, want Parse and 7", got.Query, got.Total)
	}
}

func TestFindLimitCutsTheListButNotTheTotal(t *testing.T) {
	x := named("Parse", "parse", "PARSE", "ParseFile", "Reparse")
	got := x.Find("parse", 3)
	if want := []string{"parse", "Parse", "PARSE"}; !slices.Equal(names(got.Symbols), want) {
		t.Errorf("Find(parse, 3) = %v, want %v", names(got.Symbols), want)
	}
	if got.Total != 5 {
		t.Errorf("total = %d, want 5", got.Total)
	}
	if all := x.Find("parse", 0); len(all.Symbols) != 5 {
		t.Errorf("limit 0 gave %d symbols, want all 5", len(all.Symbols))
	}
}

func TestFindWithNothingToMatchIsAnEmptyList(t *testing.T) {
	x := named("Parse")
	for _, q := range []string{"", "zzz"} {
		got := x.Find(q, 10)
		if got.Symbols == nil || len(got.Symbols) != 0 || got.Total != 0 {
			t.Errorf("Find(%q) = %#v, want an empty list", q, got)
		}
	}
}

func TestFindSkipsSymbolsWithNoNameAndLocals(t *testing.T) {
	x := New(&model.Index{
		Symbols:   []model.Symbol{{ID: "opaque"}, {ID: "s m p v Real#", DisplayName: "Real"}},
		Documents: []model.Document{{Path: "a.go", Locals: []model.Symbol{{ID: "local 0", DisplayName: "Real"}}}},
	})
	if got := x.Find("Real", 0); len(got.Symbols) != 1 || got.Symbols[0].ID != "s m p v Real#" {
		t.Errorf("Find(Real) = %+v", got.Symbols)
	}
	if got := x.Find("opaque", 0); got.Total != 0 {
		t.Errorf("a symbol string that cannot be read has no name to find: %+v", got)
	}
}

func TestFindOnTheFixtureSeesUndescribedAndDuplicateNames(t *testing.T) {
	x := fixture(t)
	got := x.Find("A", 0)
	// The function A and the method Thing#A are both displayed as A, in ID order.
	if want := []string{sciptest.FnA, sciptest.ThingA}; !slices.Equal(ids(got.Symbols), want) {
		t.Errorf("Find(A) = %v, want %v", ids(got.Symbols), want)
	}
	// Remote has no symbol information anywhere; its name comes from the symbol string.
	got = x.Find("Remote", 0)
	if len(got.Symbols) != 1 || got.Symbols[0].ID != sciptest.FnRemote || got.Symbols[0].Path != "" {
		t.Errorf("Find(Remote) = %+v, want the undescribed symbol, with no location", got.Symbols)
	}
	if got := x.Find("emot", 0); got.Total != 1 {
		t.Errorf("Find(emot) total = %d, want the substring match on Remote", got.Total)
	}
	if got := x.Find("x", 0); got.Total != 0 {
		t.Errorf("Find(x) = %+v: local 0 is called x but locals are not searched", got)
	}
}

func ids(syms []Symbol) []string {
	out := []string{}
	for _, s := range syms {
		out = append(out, s.ID)
	}
	return out
}

func TestResolveTakesASymbolStringOrAUniqueName(t *testing.T) {
	x := fixture(t)
	for _, tc := range []struct{ in, want string }{
		{sciptest.FnRec, sciptest.FnRec},
		{sciptest.FnRemote, sciptest.FnRemote},
		{"Rec", sciptest.FnRec},
		{"rec", sciptest.FnRec},
		{"Remote", sciptest.FnRemote},
		{"Thing", sciptest.Thing},
	} {
		got, err := x.Resolve(tc.in)
		if err != nil || got.ID != tc.want {
			t.Errorf("Resolve(%q) = %+v, %v; want %s", tc.in, got, err, tc.want)
		}
	}
}

func TestResolveDoesNotGuessFromAPrefixOrAFragment(t *testing.T) {
	for _, in := range []string{"Re", "emot", "nothing", "local 0", ""} {
		if _, err := fixture(t).Resolve(in); !errors.Is(err, ErrNoSymbol) {
			t.Errorf("Resolve(%q) err = %v, want ErrNoSymbol", in, err)
		}
	}
}

func TestResolvePrefersTheExactCaseOverACaseInsensitiveMatch(t *testing.T) {
	x := named("Parse", "parse")
	got, err := x.Resolve("Parse")
	if err != nil || got.Name != "Parse" {
		t.Errorf("Resolve(Parse) = %+v, %v", got, err)
	}
	got, err = x.Resolve("PARSE")
	var amb *AmbiguousError
	if !errors.As(err, &amb) || amb.Total != 2 {
		t.Errorf("Resolve(PARSE) = %+v, %v; want both spellings as candidates", got, err)
	}
}

func TestResolveRefusesAnAmbiguousNameAndListsTheCandidates(t *testing.T) {
	_, err := fixture(t).Resolve("A")
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want an AmbiguousError", err)
	}
	if amb.Name != "A" || amb.Total != 2 || !slices.Equal(ids(amb.Candidates), []string{sciptest.FnA, sciptest.ThingA}) {
		t.Errorf("ambiguity = %+v", amb)
	}
	for _, want := range []string{`"A"`, sciptest.FnA, sciptest.ThingA, "calls/a.go:2", "calls/b.go:19"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err.Error(), want)
		}
	}
}

func TestResolveShowsOnlyTheFirstCandidates(t *testing.T) {
	same := make([]string, 25)
	for i := range same {
		same[i] = "New"
	}
	_, err := named(same...).Resolve("New")
	var amb *AmbiguousError
	if !errors.As(err, &amb) || amb.Total != 25 || len(amb.Candidates) != MaxCandidates {
		t.Fatalf("err = %v; want 25 matches with %d listed", err, MaxCandidates)
	}
	if !strings.Contains(err.Error(), "5 more") {
		t.Errorf("message %q does not say how many were left out", err.Error())
	}
}
