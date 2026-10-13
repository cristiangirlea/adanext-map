package query

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// Caller is a definition that holds references to the symbol asked about, and where in it they are.
// Symbol says where the caller is defined; Sites are lines of the same file.
type Caller struct {
	Symbol Symbol `json:"symbol"`
	Sites  []Site `json:"sites"`
}

// CallersResult is the answer to Callers. Unattributed counts references that lie in no definition,
// such as a package-level initializer, and so have no caller to report.
type CallersResult struct {
	Symbol       Symbol   `json:"symbol"`
	Callers      []Caller `json:"callers"`
	Unattributed int      `json:"unattributed,omitempty"`
}

// pos is a position in a document, compared by line and then column.
type pos struct{ line, col int }

func (p pos) less(q pos) bool { return p.line < q.line || p.line == q.line && p.col < q.col }

func startOf(r model.Range) pos { return pos{r.StartLine, r.StartCol} }
func endOf(r model.Range) pos   { return pos{r.EndLine, r.EndCol} }

// endOfDocument is after every position.
var endOfDocument = pos{math.MaxInt, 0}

// scope is the part of a document a definition holds: where a reference counts as being in it.
type scope struct {
	start, end pos // end is exclusive
	// parent is the index of the nearest scope in the same document that holds this one, or -1.
	parent int32
	// up is the index of the nearest scope around this one that ends after it does, or -1, so the
	// ends grow along the chain of up links. depth counts the links to the end of the chain, and jump
	// is a skip pointer along it (a skew-binary jump pointer, the end of the chain pointing at itself),
	// which lets endingAfter search the chain in logarithmic time.
	up, jump, depth int32
	// occ is the definition occurrence, by index in its document.
	occ int32
	// fallback marks a scope made without an enclosing range, so clipped to its parent.
	fallback bool
}

func (s *scope) contains(p pos) bool { return !p.less(s.start) && p.less(s.end) }

// buildScopes makes the scopes of the global definitions in d, given their occurrence indexes.
//
// A definition's scope is the enclosing range of its occurrence. When the indexer gave none, it is
// the span from column 0 of the line of the definition's name to column 0 of the line of the next
// definition's name in the document (to the end of the document for the last one), clipped to the
// scope around it if there is one. Only global definitions count, as boundaries as well as scopes:
// a local variable inside a function must not end the function.
func buildScopes(d *model.Document, defs []int32) []scope {
	if len(defs) == 0 {
		return nil
	}
	byName := slices.Clone(defs)
	slices.SortStableFunc(byName, func(a, b int32) int {
		return compareRange(d.Occurrences[a].Range, d.Occurrences[b].Range)
	})
	scopes := make([]scope, len(byName))
	// A fallback scope starts at column 0 of its name's line, so that what stands left of the name
	// (a Go receiver, a return type) is inside it. A second definition on that line starts at its name.
	fallbackStart := func(i int) pos {
		start := startOf(d.Occurrences[byName[i]].Range)
		if i > 0 && d.Occurrences[byName[i-1]].Range.StartLine == start.line {
			return start
		}
		return pos{start.line, 0}
	}
	// next is where the nearest later definition that starts after this one begins.
	next := endOfDocument
	for i := len(byName) - 1; i >= 0; i-- {
		o := &d.Occurrences[byName[i]]
		if i+1 < len(byName) {
			if startOf(o.Range).less(startOf(d.Occurrences[byName[i+1]].Range)) {
				next = fallbackStart(i + 1)
			}
		}
		s := scope{occ: byName[i], parent: -1}
		if o.Enclosing != nil {
			s.start, s.end = startOf(*o.Enclosing), endOf(*o.Enclosing)
		} else {
			s.start, s.end, s.fallback = fallbackStart(i), next, true
		}
		scopes[i] = s
	}
	// Outer scopes first: by start, and for the same start the larger one.
	slices.SortStableFunc(scopes, func(a, b scope) int {
		if a.start != b.start {
			if a.start.less(b.start) {
				return -1
			}
			return 1
		}
		if a.end != b.end {
			if b.end.less(a.end) {
				return -1
			}
			return 1
		}
		return 0
	})
	// One sweep gives each scope its parent: the nearest earlier scope still open at its start.
	var open []int32
	for i := range scopes {
		s := &scopes[i]
		for len(open) > 0 && !s.start.less(scopes[open[len(open)-1]].end) {
			open = open[:len(open)-1]
		}
		up := int32(-1)
		if len(open) > 0 {
			s.parent = open[len(open)-1]
			if p := &scopes[s.parent]; s.fallback && p.end.less(s.end) {
				s.end = p.end
			}
			up = endingAfter(scopes, s.parent, s.end)
		}
		linkUp(scopes, int32(i), up)
		open = append(open, int32(i))
	}
	return scopes
}

// linkUp makes scopes[i] the next link of the up chain of scopes[u], or the end of a chain if u is -1.
func linkUp(scopes []scope, i, u int32) {
	s := &scopes[i]
	s.up, s.jump, s.depth = u, i, 0
	if u < 0 {
		return
	}
	p := &scopes[u]
	s.depth = p.depth + 1
	if j := &scopes[p.jump]; p.depth-j.depth == j.depth-scopes[j.jump].depth {
		s.jump = j.jump
	} else {
		s.jump = u
	}
}

// endingAfter is the first scope on the up chain from scopes[j], that one included, to end after p,
// or -1. The ends grow along the chain, so the search skips ahead whenever the skip pointer lands on
// a scope that does not end after p either.
func endingAfter(scopes []scope, j int32, p pos) int32 {
	for j >= 0 && !p.less(scopes[j].end) {
		if k := scopes[j].jump; k != j && !p.less(scopes[k].end) {
			j = k
		} else {
			j = scopes[j].up
		}
	}
	return j
}

// scopeAt is the innermost scope of document di that holds p: the last scope starting at or before
// p, or the nearest one around it that ends after p. Walking from the first outward one scope at a
// time would cost the depth of the nesting for each reference, and a crafted store can nest as deep
// as it has definitions.
func (x *Index) scopeAt(di int32, p pos) *scope {
	scopes := x.scopes[di]
	i := sort.Search(len(scopes), func(i int) bool { return p.less(scopes[i].start) })
	if j := endingAfter(scopes, int32(i)-1, p); j >= 0 {
		return &scopes[j]
	}
	return nil
}

// callerKey identifies a caller by the definition occurrence that holds the reference.
type callerKey struct{ doc, occ int32 }

// holders calls f for each reference to id that lies in a definition, with the scope of the
// innermost one, and returns how many references lie in none. A reference is an occurrence that is
// not a definition and not an import.
func (x *Index) holders(id string, f func(r occRef, sc *scope)) (unattributed int) {
	for _, r := range x.occs[id] {
		o := &x.src.Documents[r.doc].Occurrences[r.occ]
		if o.Roles.Has(model.RoleDefinition) || o.Roles.Has(model.RoleImport) {
			continue
		}
		if sc := x.scopeAt(r.doc, startOf(o.Range)); sc != nil {
			f(r, sc)
		} else {
			unattributed++
		}
	}
	return unattributed
}

// callersOf finds the definitions holding references to id, by place, and counts the references
// that no definition holds.
func (x *Index) callersOf(id string) (callers []Caller, unattributed int) {
	byKey := map[callerKey]int{}
	unattributed = x.holders(id, func(r occRef, sc *scope) {
		key := callerKey{r.doc, sc.occ}
		i, ok := byKey[key]
		if !ok {
			def := x.src.Documents[r.doc].Occurrences[sc.occ].Symbol
			i = len(callers)
			byKey[key] = i
			callers = append(callers, Caller{Symbol: x.viewAt(x.byID[def], occRef{r.doc, sc.occ})})
		}
		callers[i].Sites = append(callers[i].Sites, x.site(r))
	})
	slices.SortFunc(callers, func(a, b Caller) int {
		return cmp.Or(strings.Compare(a.Symbol.Path, b.Symbol.Path), cmp.Compare(a.Symbol.Line, b.Symbol.Line),
			strings.Compare(a.Symbol.ID, b.Symbol.ID))
	})
	for i := range callers {
		slices.SortStableFunc(callers[i].Sites, func(a, b Site) int {
			return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
		})
	}
	return callers, unattributed
}

// Callers lists the definitions that hold a reference to the symbol id: the symbols that call or
// use it. Each reference belongs to the innermost definition around it, a definition's extent
// being the enclosing range its occurrence carries or, when the indexer gave none, the span up to
// the next definition in the document (see buildScopes). The callers come by path and line. A
// reference to id inside id's own definition makes it its own caller. An id the index does not
// know is ErrNoSymbol.
func (x *Index) Callers(id string) (CallersResult, error) {
	e := x.byID[id]
	if e == nil {
		return CallersResult{}, fmt.Errorf("%w: %q", ErrNoSymbol, id)
	}
	res := CallersResult{Symbol: x.view(e)}
	res.Callers, res.Unattributed = x.callersOf(id)
	if res.Callers == nil {
		res.Callers = []Caller{}
	}
	return res, nil
}
