package query

import (
	"errors"
	"slices"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

// levels lists the impacted symbols' names with their depths, as "name:depth".
func levels(r ImpactResult) []string {
	out := []string{}
	for _, i := range r.Impacted {
		out = append(out, i.Symbol.Name+":"+string(rune('0'+i.Depth)))
	}
	return out
}

func TestImpactWalksCallersBreadthFirstAndTagsEachWithItsDepth(t *testing.T) {
	x := fixture(t)
	// C is called by B, Rec and the method Thing#A (depth 1); B by A, Rec by Thing (depth 2); A by D.
	got, err := x.Impact(sciptest.FnC, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"B:1", "Rec:1", "A:1", "A:2", "Thing:2", "D:3"}; !slices.Equal(levels(got), want) {
		// The method Thing#A is displayed as A: depth 1 holds B, Rec and that A.
		t.Errorf("impact of C = %v, want %v", levels(got), want)
	}
	if got.Target != sciptest.FnC || got.Kind != "symbol" || got.Depth != 3 {
		t.Errorf("header = %q %q %d", got.Target, got.Kind, got.Depth)
	}
	via := map[string]string{}
	for _, i := range got.Impacted {
		via[i.Symbol.ID] = i.Via
	}
	wantVia := map[string]string{
		sciptest.FnB: sciptest.FnC, sciptest.FnRec: sciptest.FnC, sciptest.ThingA: sciptest.FnC,
		sciptest.FnA: sciptest.FnB, sciptest.Thing: sciptest.FnRec, sciptest.FnD: sciptest.FnA,
	}
	for id, v := range wantVia {
		if via[id] != v {
			t.Errorf("%s is reached via %q, want %q", id, via[id], v)
		}
	}
}

func TestImpactStopsAtTheDepthLimit(t *testing.T) {
	x := fixture(t)
	for depth, want := range map[int]int{1: 3, 2: 5, 3: 6, 50: 6} {
		got, err := x.Impact(sciptest.FnC, depth)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Impacted) != want || got.Depth != depth {
			t.Errorf("depth %d: %d symbols (%v), want %d", depth, len(got.Impacted), levels(got), want)
		}
		for _, i := range got.Impacted {
			if i.Depth > depth {
				t.Errorf("depth %d: %s is at depth %d", depth, i.Symbol.Name, i.Depth)
			}
		}
	}
}

func TestImpactDefaultsTheDepthToThree(t *testing.T) {
	for _, depth := range []int{0, -1} {
		got, err := fixture(t).Impact(sciptest.FnC, depth)
		if err != nil || got.Depth != DefaultDepth || DefaultDepth != 3 || len(got.Impacted) != 6 {
			t.Errorf("depth %d: %v, %v, reported depth %d", depth, levels(got), err, got.Depth)
		}
	}
}

func TestImpactTerminatesOnCyclesAndListsNothingTwice(t *testing.T) {
	// C and Rec call each other, and Rec calls itself.
	for _, id := range []string{sciptest.FnC, sciptest.FnRec} {
		got, err := fixture(t).Impact(id, 1000)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{id: true}
		for _, i := range got.Impacted {
			if seen[i.Symbol.ID] {
				t.Errorf("impact of %s lists %s again or lists the target itself", id, i.Symbol.Name)
			}
			seen[i.Symbol.ID] = true
		}
	}
	got, _ := fixture(t).Impact(sciptest.FnRec, 1000)
	// Callers of Rec are C and Thing; then B and the method; then A; then D.
	if want := []string{"C:1", "Thing:1", "B:2", "A:2", "A:3", "D:4"}; !slices.Equal(levels(got), want) {
		t.Errorf("impact of Rec = %v, want %v", levels(got), want)
	}
}

func TestImpactOfASymbolNobodyCallsIsEmpty(t *testing.T) {
	got, err := fixture(t).Impact(sciptest.FnD, 3)
	if err != nil || got.Impacted == nil || len(got.Impacted) != 0 {
		t.Errorf("impact of D = %#v, %v", got, err)
	}
}

func TestImpactOfASymbolNeverDefinedReachesItsCallers(t *testing.T) {
	got, err := fixture(t).Impact(sciptest.FnRemote, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"B:1", "A:2", "D:3"}; !slices.Equal(levels(got), want) {
		t.Errorf("impact of Remote = %v, want %v", levels(got), want)
	}
}

func TestImpactOfAnUnknownSymbolIsAnError(t *testing.T) {
	if _, err := fixture(t).Impact("s m p v Nope#", 3); !errors.Is(err, ErrNoSymbol) {
		t.Errorf("err = %v, want ErrNoSymbol", err)
	}
}

func TestImpactOfAFileIsTheCallersOfEverythingItDefines(t *testing.T) {
	x := fixture(t)
	// b.go defines C, Rec, Thing and the method Thing#A. They call each other, which is not impact
	// on the file; the rest of the chain is: B calls C, A calls B, D calls A.
	got, err := x.ImpactFile(sciptest.PathB, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"B:1", "A:2", "D:3"}; !slices.Equal(levels(got), want) {
		t.Errorf("impact of %s = %v, want %v", sciptest.PathB, levels(got), want)
	}
	if got.Target != sciptest.PathB || got.Kind != "file" {
		t.Errorf("header = %q %q", got.Target, got.Kind)
	}
	if got.Impacted[0].Via != sciptest.FnC {
		t.Errorf("B is reached via %q, want C", got.Impacted[0].Via)
	}
	if got, _ := x.ImpactFile(sciptest.PathB, 1); len(got.Impacted) != 1 {
		t.Errorf("depth 1 = %v", levels(got))
	}
}

func TestImpactOfAFileWithNoSymbolsIsEmpty(t *testing.T) {
	got, err := fixture(t).ImpactFile(sciptest.PathEmpty, 3)
	if err != nil || got.Impacted == nil || len(got.Impacted) != 0 {
		t.Errorf("impact = %#v, %v", got, err)
	}
}

func TestImpactOfAnUnknownFileIsAnError(t *testing.T) {
	if _, err := fixture(t).ImpactFile("calls/missing.go", 3); !errors.Is(err, ErrNoDocument) {
		t.Errorf("err = %v, want ErrNoDocument", err)
	}
}

func TestImpactCountsTheReferencesItCouldNotFollow(t *testing.T) {
	x := fixture(t)
	// The reference to A in b.go lies in no definition. A is reached at depth 2, and its callers are
	// looked up at depth 3, so only a walk that deep meets that reference.
	for depth, want := range map[int]int{1: 0, 2: 0, 3: 1} {
		got, err := x.Impact(sciptest.FnC, depth)
		if err != nil {
			t.Fatal(err)
		}
		if got.Unattributed != want {
			t.Errorf("impact of C at depth %d: unattributed %d, want %d", depth, got.Unattributed, want)
		}
	}
	// The target's own references count too, and a file's walk starts from its symbols.
	got, err := x.Impact(sciptest.FnA, 1)
	if err != nil || got.Unattributed != 1 {
		t.Errorf("impact of A: %+v, %v", got, err)
	}
	got, err = x.ImpactFile(sciptest.PathA, 1)
	if err != nil || got.Unattributed != 1 {
		t.Errorf("impact of a.go: %+v, %v", got, err)
	}
}
