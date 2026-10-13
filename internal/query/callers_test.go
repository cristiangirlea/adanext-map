package query

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func lines(sites []Site) []int {
	out := []int{}
	for _, s := range sites {
		out = append(out, s.Line)
	}
	return out
}

func callerIDs(cs []Caller) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Symbol.ID)
	}
	return out
}

func TestCallersUsesTheEnclosingRangeOfTheDefinition(t *testing.T) {
	// A has an enclosing range (lines 2 to 6) and calls B on line 4.
	got, err := fixture(t).Callers(sciptest.FnB)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Callers) != 1 || got.Callers[0].Symbol.ID != sciptest.FnA || !slices.Equal(lines(got.Callers[0].Sites), []int{4}) {
		t.Errorf("callers of B = %+v", got.Callers)
	}
	if got.Symbol.ID != sciptest.FnB || got.Unattributed != 0 {
		t.Errorf("symbol %+v, unattributed %d", got.Symbol, got.Unattributed)
	}
	if c := got.Callers[0].Symbol; c.Path != sciptest.PathA || c.Line != 2 || c.EndLine != 6 {
		t.Errorf("caller is defined at %+v", c)
	}
}

func TestCallersPicksTheInnermostDefinitionAndSortsThemByPlace(t *testing.T) {
	got, err := fixture(t).Callers(sciptest.FnC)
	if err != nil {
		t.Fatal(err)
	}
	// B has no enclosing range (a.go:8), Rec has one (b.go:8), and ThingA's lies inside Thing's.
	if want := []string{sciptest.FnB, sciptest.FnRec, sciptest.ThingA}; !slices.Equal(callerIDs(got.Callers), want) {
		t.Fatalf("callers of C = %v, want %v", callerIDs(got.Callers), want)
	}
	if !slices.Equal(lines(got.Callers[0].Sites), []int{10}) || !slices.Equal(lines(got.Callers[2].Sites), []int{20}) {
		t.Errorf("sites = %+v", got.Callers)
	}
}

func TestCallersAttributesAReferenceToTheInnermostEnclosingDefinitionOnly(t *testing.T) {
	got, err := fixture(t).Callers(sciptest.FnRec)
	if err != nil {
		t.Fatal(err)
	}
	// C calls Rec; Rec calls itself; the reference on line 22 is in Thing, after its method ThingA.
	want := map[string][]int{sciptest.FnC: {4}, sciptest.FnRec: {10}, sciptest.Thing: {22}}
	if len(got.Callers) != len(want) {
		t.Fatalf("callers of Rec = %+v", got.Callers)
	}
	for _, c := range got.Callers {
		if !slices.Equal(lines(c.Sites), want[c.Symbol.ID]) {
			t.Errorf("%s: sites %v, want %v", c.Symbol.Name, lines(c.Sites), want[c.Symbol.ID])
		}
	}
}

func TestCallersFallBackToTheSpanToTheNextDefinitionWhenThereIsNoEnclosingRange(t *testing.T) {
	x := fixture(t)
	// B has no enclosing range and a local variable inside it: the span runs to D, not to the local.
	got, err := x.Callers(sciptest.FnRemote)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Callers) != 1 || got.Callers[0].Symbol.ID != sciptest.FnB || got.Symbol.Path != "" {
		t.Errorf("callers of Remote = %+v", got.Callers)
	}
	// D is the last definition in a.go, so its span runs to the end of the document. The reference
	// to A in b.go is in no definition at all.
	got, err = x.Callers(sciptest.FnA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Callers) != 1 || got.Callers[0].Symbol.ID != sciptest.FnD || got.Unattributed != 1 {
		t.Errorf("callers of A = %+v, unattributed %d", got.Callers, got.Unattributed)
	}
}

func TestCallersOfASymbolNobodyCallsIsEmpty(t *testing.T) {
	got, err := fixture(t).Callers(sciptest.FnD)
	if err != nil || got.Callers == nil || len(got.Callers) != 0 {
		t.Errorf("callers of D = %#v, %v", got, err)
	}
}

func TestCallersOfAnUnknownSymbolIsAnError(t *testing.T) {
	if _, err := fixture(t).Callers("s m p v Nope#"); !errors.Is(err, ErrNoSymbol) {
		t.Errorf("err = %v, want ErrNoSymbol", err)
	}
}

// occSpec is one occurrence of a single-line name, for an index of one document.
type occSpec struct {
	sym       string
	line, col int
	def       bool
	enclosing *model.Range
	roles     model.Role
}

func oneDoc(specs ...occSpec) *Index {
	var occs []model.Occurrence
	for _, s := range specs {
		roles := model.RoleReference
		if s.def {
			roles = model.RoleDefinition
		}
		occs = append(occs, model.Occurrence{
			Range: rng(s.line, s.col, s.col+1), Symbol: s.sym, Roles: roles | s.roles, Enclosing: s.enclosing,
		})
	}
	return New(&model.Index{Documents: []model.Document{{Path: "x.go", Occurrences: occs}}})
}

func span(l0, c0, l1, c1 int) *model.Range {
	return &model.Range{StartLine: l0, StartCol: c0, EndLine: l1, EndCol: c1}
}

func TestCallersFallbackStopsAtTheNextDefinitionAndNotBefore(t *testing.T) {
	const f, g, target = "s m p v f().", "s m p v g().", "s m p v target()."
	x := oneDoc(
		occSpec{sym: target, line: 0, col: 0},        // before every definition
		occSpec{sym: f, line: 2, col: 5, def: true},  // f: from line 2 to where g starts
		occSpec{sym: target, line: 3, col: 1},        // in f
		occSpec{sym: target, line: 5, col: 9},        // in g: a scope starts at column 0 of its name's line
		occSpec{sym: g, line: 5, col: 20, def: true}, // g starts on that line, further right
		occSpec{sym: target, line: 5, col: 25},       // in g
		occSpec{sym: target, line: 40, col: 0},       // in g: the last definition runs to the end
	)
	got, err := x.Callers(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unattributed != 1 {
		t.Errorf("unattributed = %d, want the reference before f", got.Unattributed)
	}
	if want := []string{f, g}; !slices.Equal(callerIDs(got.Callers), want) {
		t.Fatalf("callers = %v, want %v", callerIDs(got.Callers), want)
	}
	if !slices.Equal(lines(got.Callers[0].Sites), []int{4}) || !slices.Equal(lines(got.Callers[1].Sites), []int{6, 6, 41}) {
		t.Errorf("sites: f %v, g %v", lines(got.Callers[0].Sites), lines(got.Callers[1].Sites))
	}
}

func TestCallersEnclosingRangeIncludesItsStartAndExcludesItsEnd(t *testing.T) {
	const f, target = "s m p v f().", "s m p v target()."
	x := oneDoc(
		occSpec{sym: f, line: 1, col: 5, def: true, enclosing: span(1, 0, 3, 1)},
		occSpec{sym: target, line: 1, col: 0}, // the first column of the construct
		occSpec{sym: target, line: 3, col: 0}, // the last column inside it
		occSpec{sym: target, line: 3, col: 1}, // the end is exclusive: outside
		occSpec{sym: target, line: 0, col: 9}, // before it
	)
	got, err := x.Callers(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Callers) != 1 || !slices.Equal(lines(got.Callers[0].Sites), []int{2, 4}) || got.Unattributed != 2 {
		t.Errorf("callers = %+v, unattributed %d", got.Callers, got.Unattributed)
	}
}

func TestCallersFallbackStaysInsideTheDefinitionThatEncloses(t *testing.T) {
	const outer, inner, target = "s m p v Outer#", "s m p v inner().", "s m p v target()."
	x := oneDoc(
		occSpec{sym: outer, line: 0, col: 5, def: true, enclosing: span(0, 0, 9, 1)},
		occSpec{sym: inner, line: 2, col: 1, def: true}, // no enclosing range: runs to the end, clipped to Outer
		occSpec{sym: target, line: 4, col: 1},           // in inner
		occSpec{sym: target, line: 20, col: 1},          // past Outer: in no definition
	)
	got, err := x.Callers(target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{inner}; !slices.Equal(callerIDs(got.Callers), want) || got.Unattributed != 1 {
		t.Errorf("callers = %v, unattributed %d", callerIDs(got.Callers), got.Unattributed)
	}
}

func TestCallersIgnoresLocalDefinitionsAsScopesAndImportsAsCalls(t *testing.T) {
	const f, target = "s m p v f().", "s m p v target()."
	x := oneDoc(
		occSpec{sym: f, line: 1, col: 5, def: true},
		occSpec{sym: "local 0", line: 2, col: 1, def: true, enclosing: span(2, 0, 2, 9)},
		occSpec{sym: target, line: 3, col: 1},                          // the local does not close f
		occSpec{sym: target, line: 3, col: 5, roles: model.RoleImport}, // an import is not a call
	)
	got, err := x.Callers(target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{f}; !slices.Equal(callerIDs(got.Callers), want) ||
		!slices.Equal(lines(got.Callers[0].Sites), []int{4}) || got.Unattributed != 0 {
		t.Errorf("callers = %+v, unattributed %d", got.Callers, got.Unattributed)
	}
}

func TestCallersDoesNotUseTheReferencesOwnEnclosingRange(t *testing.T) {
	const f, target = "s m p v f().", "s m p v target()."
	// The reference's enclosing range is the call expression around it; it says nothing of the caller.
	x := oneDoc(
		occSpec{sym: f, line: 1, col: 5, def: true, enclosing: span(1, 0, 4, 1)},
		occSpec{sym: target, line: 9, col: 1, enclosing: span(1, 0, 9, 5)},
	)
	got, err := x.Callers(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Callers) != 0 || got.Unattributed != 1 {
		t.Errorf("callers = %+v, unattributed %d", got.Callers, got.Unattributed)
	}
}

func TestCallersFallbackScopeStartsAtTheStartOfTheNamesLine(t *testing.T) {
	// Go's `func (s *Server) Handle()`: the reference to Server sits left of the name Handle.
	const prev, handle, server = "s m p v prev().", "s m p v Server#Handle().", "s m p v Server#"
	x := oneDoc(
		occSpec{sym: prev, line: 0, col: 5, def: true},
		occSpec{sym: server, line: 4, col: 9},
		occSpec{sym: handle, line: 4, col: 17, def: true},
	)
	got, err := x.Callers(server)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{handle}; !slices.Equal(callerIDs(got.Callers), want) || got.Unattributed != 0 {
		t.Errorf("callers = %v, unattributed %d, want %v and none", callerIDs(got.Callers), got.Unattributed, want)
	}
	// With no definition before it, the reference is not left unattributed either.
	x = oneDoc(
		occSpec{sym: server, line: 0, col: 9},
		occSpec{sym: handle, line: 0, col: 17, def: true},
	)
	got, err = x.Callers(server)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{handle}; !slices.Equal(callerIDs(got.Callers), want) || got.Unattributed != 0 {
		t.Errorf("first definition: callers = %v, unattributed %d, want %v and none", callerIDs(got.Callers), got.Unattributed, want)
	}
}

// TestScopeAtAgreesWithWalkingTheParentsOnOverlappingRanges checks the search along the up chain
// against the plain walk along the parent links, on random enclosing ranges that nest and overlap.
func TestScopeAtAgreesWithWalkingTheParentsOnOverlappingRanges(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	for round := range 200 {
		var occs []model.Occurrence
		for k := range 1 + rnd.IntN(40) {
			l0, c0 := rnd.IntN(30), rnd.IntN(4)
			l1, c1 := l0+rnd.IntN(12), rnd.IntN(4)
			var enclosing *model.Range
			if rnd.IntN(4) > 0 {
				enclosing = span(l0, c0, l1, c1)
			}
			occs = append(occs, model.Occurrence{
				Range: rng(l0+rnd.IntN(3), 5, 6), Symbol: fmt.Sprintf("s m p v f%d().", k),
				Roles: model.RoleDefinition, Enclosing: enclosing,
			})
		}
		x := New(&model.Index{Documents: []model.Document{{Path: "x.go", Occurrences: occs}}})
		scopes := x.scopes[0]
		for range 200 {
			p := pos{rnd.IntN(45), rnd.IntN(8)}
			var want *scope
			i := sort.Search(len(scopes), func(i int) bool { return p.less(scopes[i].start) })
			for j := int32(i) - 1; j >= 0; j = scopes[j].parent {
				if scopes[j].contains(p) {
					want = &scopes[j]
					break
				}
			}
			if got := x.scopeAt(0, p); got != want {
				t.Fatalf("round %d: scopeAt(%v) = %+v, walking the parents gives %+v", round, p, got, want)
			}
		}
	}
}
