package query

import (
	"errors"
	"slices"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

// fixture is the call-graph fixture, converted as `adamap index` would and indexed for queries.
func fixture(t testing.TB) *Index {
	t.Helper()
	m, err := scip.Convert(sciptest.CallGraph())
	if err != nil {
		t.Fatal(err)
	}
	return New(m)
}

// names are the display names of syms, in order.
func names(syms []Symbol) []string {
	out := []string{}
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}

func TestOutlineListsWhatADocumentDefinesByLine(t *testing.T) {
	x := fixture(t)
	got, err := x.Outline(sciptest.PathA)
	if err != nil {
		t.Fatal(err)
	}
	want := []Symbol{
		{ID: sciptest.FnA, Name: "A", Kind: "Function", Path: sciptest.PathA, Line: 2, EndLine: 6, Signature: "func A()"},
		{ID: sciptest.FnB, Name: "B", Kind: "Function", Path: sciptest.PathA, Line: 8, EndLine: 8},
		{ID: sciptest.FnD, Name: "D", Kind: "Function", Path: sciptest.PathA, Line: 14, EndLine: 14},
	}
	if !slices.Equal(got, want) {
		t.Errorf("outline of %s:\n got %+v\nwant %+v", sciptest.PathA, got, want)
	}

	got, err = x.Outline(sciptest.PathB)
	if err != nil {
		t.Fatal(err)
	}
	// ThingA is displayed as "A" and sits inside Thing: nesting is by line, not by tree.
	if want := []string{"C", "Rec", "Thing", "A"}; !slices.Equal(names(got), want) {
		t.Errorf("outline of %s = %v, want %v", sciptest.PathB, names(got), want)
	}
	if last := got[3]; last.ID != sciptest.ThingA || last.Kind != "Method" || last.Line != 19 || last.EndLine != 21 {
		t.Errorf("method entry = %+v", last)
	}
}

func TestOutlineLeavesOutLocalSymbols(t *testing.T) {
	// a.go defines local 0 and local 1; they are variables of A and B, not part of the file's shape.
	got, err := fixture(t).Outline(sciptest.PathA)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.Name == "x" || s.Name == "y" {
			t.Errorf("outline lists the local %+v", s)
		}
	}
}

func TestOutlineOfADocumentWithoutSymbolsIsEmptyNotAnError(t *testing.T) {
	got, err := fixture(t).Outline(sciptest.PathEmpty)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("outline = %#v, %v; want an empty list and no error", got, err)
	}
}

func TestOutlineOfAnUnknownDocumentIsAnError(t *testing.T) {
	_, err := fixture(t).Outline("calls/missing.go")
	if !errors.Is(err, ErrNoDocument) {
		t.Errorf("err = %v, want ErrNoDocument", err)
	}
}

func TestOutlineSortsByLineEvenWhenTheStoreDoesNot(t *testing.T) {
	def := model.RoleDefinition
	x := New(&model.Index{Documents: []model.Document{{Path: "x.go", Occurrences: []model.Occurrence{
		{Range: model.Range{StartLine: 9, StartCol: 1, EndLine: 9, EndCol: 2}, Symbol: "s m p v Late#", Roles: def},
		{Range: model.Range{StartLine: 2, StartCol: 1, EndLine: 2, EndCol: 2}, Symbol: "s m p v Early#", Roles: def},
	}}}})
	got, err := x.Outline("x.go")
	if err != nil {
		t.Fatal(err)
	}
	// Neither symbol has symbol information: the names come from the symbol strings.
	if want := []string{"Early", "Late"}; !slices.Equal(names(got), want) {
		t.Errorf("outline = %v, want %v", names(got), want)
	}
}

func TestAnEmptyIndexAnswersWithoutPanicking(t *testing.T) {
	x := New(&model.Index{})
	if _, err := x.Outline("a.go"); !errors.Is(err, ErrNoDocument) {
		t.Errorf("outline err = %v", err)
	}
	if x.HasDocument("a.go") {
		t.Error("HasDocument is true on an empty index")
	}
}
