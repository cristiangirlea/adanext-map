package query

import (
	"fmt"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

const (
	perfDocs   = 400
	perfPerDoc = 50
	perfRefs   = 9 // references in each function; with its definition, 10 occurrences per function
)

func perfID(g int) string { return fmt.Sprintf("s m p v f%06d().", g) }

func perfPath(d int) string { return fmt.Sprintf("pkg/d%03d.go", d) }

// synthetic is an index of perfDocs files of perfPerDoc functions, 200,000 occurrences in all.
// Function g is defined with an enclosing range, except every third one, which has none so that the
// callers fallback is measured, and holds perfRefs references to other functions;
// every fourth one calls function 0, the hot symbol, so it has about 5,000 callers.
func synthetic() *model.Index {
	n := perfDocs * perfPerDoc
	idx := &model.Index{}
	for d := range perfDocs {
		doc := model.Document{Path: perfPath(d)}
		for f := range perfPerDoc {
			g := d*perfPerDoc + f
			top := f * 12
			def := model.Range{StartLine: top, StartCol: 5, EndLine: top, EndCol: 12}
			body := model.Range{StartLine: top, EndLine: top + 10, EndCol: 1}
			defn := model.Occurrence{Range: def, Symbol: perfID(g), Roles: model.RoleDefinition, Enclosing: &body}
			if f%3 == 0 {
				defn.Enclosing = nil
			}
			doc.Occurrences = append(doc.Occurrences, defn)
			for k := range perfRefs {
				target := (g*7 + k*13 + 1) % n
				if k == 0 && g%4 == 0 {
					target = 0
				}
				at := model.Range{StartLine: top + 1 + k, StartCol: 1, EndLine: top + 1 + k, EndCol: 8}
				doc.Occurrences = append(doc.Occurrences, model.Occurrence{
					Range: at, Symbol: perfID(target), Roles: model.RoleReference,
				})
			}
			idx.Symbols = append(idx.Symbols, model.Symbol{
				ID: perfID(g), DisplayName: fmt.Sprintf("Func%d", g), Kind: "Function",
				Definition: &model.Location{Path: doc.Path, Range: def},
			})
		}
		idx.Documents = append(idx.Documents, doc)
	}
	return idx
}

// fastest runs f a few times and returns the best time: a slow neighbour on a busy runner should
// not fail the test, a slow algorithm should.
func fastest(f func()) time.Duration {
	best := time.Duration(1<<63 - 1)
	for range 5 {
		start := time.Now()
		f()
		best = min(best, time.Since(start))
	}
	return best
}

func TestQueriesAnswerWellUnder50msOnTwoHundredThousandOccurrences(t *testing.T) {
	idx := synthetic()
	if got := idx.Counts().Occurrences; got != 200000 {
		t.Fatalf("the synthetic index has %d occurrences, want 200000", got)
	}
	var x *Index
	t.Logf("New: %v", fastest(func() { x = New(idx) }))

	hot, typical := perfID(0), perfID(12345)
	check := func(name string, ok bool, f func()) {
		t.Helper()
		if !ok {
			t.Errorf("%s did not return what the synthetic index holds", name)
		}
		d := fastest(f)
		t.Logf("%s: %v", name, d)
		if enforceBound && d > 50*time.Millisecond {
			t.Errorf("%s took %v, want well under 50ms", name, d)
		}
	}

	out, err := x.Outline(perfPath(200))
	check("outline", err == nil && len(out) == perfPerDoc, func() { x.Outline(perfPath(200)) })

	check("find substring", x.Find("unc123", 25).Total > 100, func() { x.Find("unc123", 25) })
	check("find prefix", x.Find("Func1", 25).Total > 1000, func() { x.Find("Func1", 25) })
	check("find miss", x.Find("zzz", 25).Total == 0, func() { x.Find("zzz", 25) })
	check("resolve", func() bool { s, err := x.Resolve("Func777"); return err == nil && s.ID == perfID(777) }(),
		func() { x.Resolve("Func777") })

	refs, err := x.Refs(hot)
	check("refs of the hot symbol", err == nil && refs.Total > 5000, func() { x.Refs(hot) })

	callers, err := x.Callers(hot)
	check("callers of the hot symbol", err == nil && near(len(callers.Callers), perfDocs*perfPerDoc/4),
		func() { x.Callers(hot) })

	imp, err := x.Impact(typical, 3)
	check("impact of a typical symbol", err == nil && len(imp.Impacted) > 0, func() { x.Impact(typical, 3) })

	imp, err = x.Impact(hot, 1)
	check("impact of the hot symbol, one level", err == nil && near(len(imp.Impacted), perfDocs*perfPerDoc/4),
		func() { x.Impact(hot, 1) })

	imp, err = x.ImpactFile(perfPath(7), 3)
	check("impact of a file", err == nil && len(imp.Impacted) > 0, func() { x.ImpactFile(perfPath(7), 3) })
}

func BenchmarkNew(b *testing.B) {
	idx := synthetic()
	b.ResetTimer()
	for range b.N {
		New(idx)
	}
}

func BenchmarkQueries(b *testing.B) {
	x := New(synthetic())
	hot, typical := perfID(0), perfID(12345)
	for name, f := range map[string]func(){
		"Outline":      func() { x.Outline(perfPath(200)) },
		"Find":         func() { x.Find("unc123", 25) },
		"RefsHot":      func() { x.Refs(hot) },
		"CallersHot":   func() { x.Callers(hot) },
		"ImpactTyp3":   func() { x.Impact(typical, 3) },
		"ImpactHotAll": func() { x.Impact(hot, 3) },
	} {
		b.Run(name, func(b *testing.B) {
			for range b.N {
				f()
			}
		})
	}
}

// near reports whether n is within 2% of want: the synthetic pattern adds a few callers by chance.
func near(n, want int) bool { return n >= want-want/50 && n <= want+want/50 }

// TestCallersOnDeeplyNestedScopesDoNotWalkTheWholeNest is a crafted store: depth definitions, each
// with an enclosing range inside the last one's, and as many references past the end of all but the
// outermost. Finding the holder of each by walking the scopes outward would cost depth squared.
func TestCallersOnDeeplyNestedScopesDoNotWalkTheWholeNest(t *testing.T) {
	const depth, target = 40000, "s m p v target()."
	var occs []model.Occurrence
	for k := range depth {
		occs = append(occs, model.Occurrence{
			Range: rng(k, 5, 8), Symbol: fmt.Sprintf("s m p v f%d().", k), Roles: model.RoleDefinition,
			Enclosing: span(k, 0, 2*depth-k, 0),
		})
	}
	for k := range depth {
		occs = append(occs, model.Occurrence{Range: rng(2*depth-1, k, k+1), Symbol: target, Roles: model.RoleReference})
	}
	x := New(&model.Index{Documents: []model.Document{{Path: "x.go", Occurrences: occs}}})
	start := time.Now()
	got, err := x.Callers(target)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Callers) != 1 || got.Callers[0].Symbol.ID != "s m p v f0()." || len(got.Callers[0].Sites) != depth || got.Unattributed != 0 {
		t.Fatalf("callers = %d, unattributed %d, want the outermost definition alone with %d sites",
			len(got.Callers), got.Unattributed, depth)
	}
	t.Logf("callers over %d nested scopes: %v", depth, took)
	if enforceBound && took > time.Second {
		t.Errorf("callers took %v over %d nested scopes, want well under a second", took, depth)
	}
}
