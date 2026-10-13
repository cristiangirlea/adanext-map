package query

import (
	"errors"
	"slices"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func rng(line, c0, c1 int) model.Range {
	return model.Range{StartLine: line, StartCol: c0, EndLine: line, EndCol: c1}
}

func TestRefsGroupsByFileWithDefinitionsFirst(t *testing.T) {
	got, err := fixture(t).Refs(sciptest.FnC)
	if err != nil {
		t.Fatal(err)
	}
	if got.Symbol.ID != sciptest.FnC || got.Symbol.Path != sciptest.PathB || got.Symbol.Line != 2 {
		t.Errorf("symbol = %+v", got.Symbol)
	}
	want := []FileRefs{
		// b.go defines C, so it comes first, although a.go sorts before it.
		{Path: sciptest.PathB, Refs: []Site{
			{Line: 2, Col: 6, Kind: "definition"},
			{Line: 11, Col: 2, Kind: "reference"},
			{Line: 20, Col: 2, Kind: "reference"},
		}},
		{Path: sciptest.PathA, Refs: []Site{{Line: 10, Col: 2, Kind: "reference"}}},
	}
	if got.Total != 4 || !slices.EqualFunc(got.Files, want, func(a, b FileRefs) bool {
		return a.Path == b.Path && slices.Equal(a.Refs, b.Refs)
	}) {
		t.Errorf("refs of C:\n got %+v (total %d)\nwant %+v", got.Files, got.Total, want)
	}
}

func TestRefsPutsADefinitionBeforeEarlierReferencesInItsFile(t *testing.T) {
	const id = "s m p v F()."
	x := New(&model.Index{Documents: []model.Document{
		{Path: "a.go", Occurrences: []model.Occurrence{{Range: rng(4, 0, 1), Symbol: id, Roles: model.RoleReference}}},
		{Path: "z.go", Occurrences: []model.Occurrence{
			{Range: rng(2, 0, 1), Symbol: id, Roles: model.RoleReference},
			{Range: rng(2, 5, 6), Symbol: id, Roles: model.RoleReference},
			{Range: rng(9, 0, 1), Symbol: id, Roles: model.RoleDefinition},
		}},
	}})
	got, err := x.Refs(id)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, f := range got.Files {
		for _, s := range f.Refs {
			order = append(order, f.Path+":"+s.Kind)
		}
	}
	if want := []string{"z.go:definition", "z.go:reference", "z.go:reference", "a.go:reference"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if z := got.Files[0].Refs; z[1].Col != 1 || z[2].Col != 6 {
		t.Errorf("the references in z.go should be by position: %+v", z)
	}
}

func TestRefsOfASymbolNeverDefinedStillFindsItsReferences(t *testing.T) {
	got, err := fixture(t).Refs(sciptest.FnRemote)
	if err != nil {
		t.Fatal(err)
	}
	if got.Symbol.Path != "" || got.Total != 1 || got.Files[0].Path != sciptest.PathA || got.Files[0].Refs[0].Line != 11 {
		t.Errorf("refs of Remote = %+v", got)
	}
}

func TestRefsNamesWhatKindOfUseEachOccurrenceIs(t *testing.T) {
	const id = "s m p v V."
	roles := []model.Role{
		model.RoleDefinition, model.RoleReference, model.RoleReference | model.RoleImport,
		model.RoleReference | model.RoleWrite, model.RoleReference | model.RoleRead,
		model.RoleDefinition | model.RoleWrite,
	}
	var occs []model.Occurrence
	for i, r := range roles {
		occs = append(occs, model.Occurrence{Range: rng(i, 0, 1), Symbol: id, Roles: r})
	}
	got, err := New(&model.Index{Documents: []model.Document{{Path: "a.go", Occurrences: occs}}}).Refs(id)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, s := range got.Files[0].Refs {
		kinds = append(kinds, s.Kind)
	}
	// Both definitions first (by line), then the rest by line.
	if want := []string{"definition", "definition", "reference", "import", "write", "read"}; !slices.Equal(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
}

func TestRefsOfASymbolWithNoOccurrencesIsEmpty(t *testing.T) {
	x := New(&model.Index{Symbols: []model.Symbol{{ID: "s m p v Lonely#", DisplayName: "Lonely"}}})
	got, err := x.Refs("s m p v Lonely#")
	if err != nil || got.Total != 0 || got.Files == nil || len(got.Files) != 0 {
		t.Errorf("refs = %#v, %v; want an empty list and no error", got, err)
	}
}

func TestRefsOfAnUnknownSymbolIsAnError(t *testing.T) {
	for _, id := range []string{"s m p v Nope#", "local 0", ""} {
		if _, err := fixture(t).Refs(id); !errors.Is(err, ErrNoSymbol) {
			t.Errorf("Refs(%q) err = %v, want ErrNoSymbol", id, err)
		}
	}
}
