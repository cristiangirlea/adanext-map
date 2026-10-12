// Package query answers outline, find, refs, callers and impact over a loaded store. New builds
// the lookup tables once, so each question reads only what it asks about: nothing here scans every
// occurrence of the index.
//
// Symbols are addressed by their SCIP symbol string. Local symbols (`local N`) are the exception:
// the same ID names a different symbol in each document, so they cannot be addressed and the
// queries leave them out. Lines in the results are one-based, as an editor shows them.
package query

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

var (
	// ErrNoDocument is returned for a path the index has no document for.
	ErrNoDocument = errors.New("no such document in the index")
	// ErrNoSymbol is returned for a symbol the index has no occurrence or description of.
	ErrNoSymbol = errors.New("no such symbol in the index")
)

// Symbol is a symbol as the queries report it. Path and the lines say where this entry is defined:
// Line is the line of its name and EndLine the last line of the construct around it, when the
// indexer gave an enclosing range. They are empty for a symbol defined outside the index.
type Symbol struct {
	ID string `json:"id"`
	// Name is the display name, or the name read out of the ID when the indexer gave none.
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// Index is a loaded store with the lookup tables the queries need. It is read-only after New and
// safe for concurrent use.
type Index struct {
	src     *model.Index
	entries []*entry // the global symbols, sorted by ID
	byID    map[string]*entry
	docs    map[string]int // document index by path
	// occs and defs hold each global symbol's occurrences, and the definitions among them, in
	// document order and then occurrence order.
	occs map[string][]occRef
	defs map[string][]occRef
}

// entry is a global symbol with the name the queries match it by.
type entry struct {
	sym   *model.Symbol
	name  string // the display name, or the one read out of the ID; empty if neither
	lower string // name in lower case
}

// occRef is an occurrence by position: document index and index within the document.
type occRef struct{ doc, occ int32 }

// New indexes idx for the queries. Symbols that occurrences name but idx never describes, such as
// a function in a library that was not indexed, get an entry of their own.
func New(idx *model.Index) *Index {
	x := &Index{
		src:  idx,
		byID: map[string]*entry{},
		docs: map[string]int{},
		occs: map[string][]occRef{},
		defs: map[string][]occRef{},
	}
	for i := range idx.Symbols {
		if s := &idx.Symbols[i]; s.ID != "" && !isLocal(s.ID) {
			x.add(s)
		}
	}
	for di := range idx.Documents {
		d := &idx.Documents[di]
		if _, dup := x.docs[d.Path]; !dup {
			x.docs[d.Path] = di
		}
		for oi := range d.Occurrences {
			o := &d.Occurrences[oi]
			if o.Symbol == "" || isLocal(o.Symbol) {
				continue
			}
			if x.byID[o.Symbol] == nil {
				x.add(&model.Symbol{ID: o.Symbol})
			}
			r := occRef{int32(di), int32(oi)}
			x.occs[o.Symbol] = append(x.occs[o.Symbol], r)
			if o.Roles.Has(model.RoleDefinition) {
				x.defs[o.Symbol] = append(x.defs[o.Symbol], r)
			}
		}
	}
	slices.SortFunc(x.entries, func(a, b *entry) int { return strings.Compare(a.sym.ID, b.sym.ID) })
	return x
}

// add records s unless a symbol with its ID is already there.
func (x *Index) add(s *model.Symbol) {
	if x.byID[s.ID] != nil {
		return
	}
	name := cmp.Or(s.DisplayName, nameFromSymbol(s.ID))
	e := &entry{sym: s, name: name, lower: strings.ToLower(name)}
	x.byID[s.ID] = e
	x.entries = append(x.entries, e)
}

// isLocal reports whether id is a local symbol, which SCIP spells `local N`.
func isLocal(id string) bool { return strings.HasPrefix(id, "local ") }

// HasDocument reports whether the index has a document at path, a slash-separated path relative
// to the project root.
func (x *Index) HasDocument(path string) bool {
	_, ok := x.docs[path]
	return ok
}

// base is e as a Symbol without a location.
func (x *Index) base(e *entry) Symbol {
	return Symbol{
		ID:        e.sym.ID,
		Name:      cmp.Or(e.name, e.sym.ID),
		Kind:      e.sym.Kind,
		Signature: e.sym.Signature,
	}
}

// viewAt is e as defined by the occurrence r.
func (x *Index) viewAt(e *entry, r occRef) Symbol {
	d := &x.src.Documents[r.doc]
	o := &d.Occurrences[r.occ]
	v := x.base(e)
	v.Path = d.Path
	v.Line = o.Range.StartLine + 1
	v.EndLine = max(lastLine(o.Range), v.Line-1) + 1
	if o.Enclosing != nil {
		v.EndLine = max(v.EndLine, lastLine(*o.Enclosing)+1)
	}
	return v
}

// view is e where the store says it is defined: its first definition occurrence.
func (x *Index) view(e *entry) Symbol {
	def := e.sym.Definition
	for _, r := range x.defs[e.sym.ID] {
		if def == nil {
			return x.viewAt(e, r)
		}
		if d := &x.src.Documents[r.doc]; d.Path == def.Path && d.Occurrences[r.occ].Range == def.Range {
			return x.viewAt(e, r)
		}
	}
	v := x.base(e)
	if def != nil {
		v.Path, v.Line, v.EndLine = def.Path, def.Range.StartLine+1, lastLine(def.Range)+1
	}
	return v
}

// lastLine is the zero-based line a range ends on: a range that ends at column 0 of a later line
// does not reach into that line.
func lastLine(r model.Range) int {
	if r.EndCol == 0 && r.EndLine > r.StartLine {
		return r.EndLine - 1
	}
	return r.EndLine
}

// Outline lists the global symbols the document at path defines, by line.
func (x *Index) Outline(path string) ([]Symbol, error) {
	di, ok := x.docs[path]
	if !ok {
		return nil, ErrNoDocument
	}
	d := &x.src.Documents[di]
	var refs []occRef
	for oi := range d.Occurrences {
		if o := &d.Occurrences[oi]; o.Roles.Has(model.RoleDefinition) && o.Symbol != "" && !isLocal(o.Symbol) {
			refs = append(refs, occRef{int32(di), int32(oi)})
		}
	}
	slices.SortStableFunc(refs, func(a, b occRef) int {
		return compareRange(d.Occurrences[a.occ].Range, d.Occurrences[b.occ].Range)
	})
	out := make([]Symbol, 0, len(refs))
	for _, r := range refs {
		out = append(out, x.viewAt(x.byID[d.Occurrences[r.occ].Symbol], r))
	}
	return out, nil
}

func compareRange(a, b model.Range) int {
	return cmp.Or(cmp.Compare(a.StartLine, b.StartLine), cmp.Compare(a.StartCol, b.StartCol),
		cmp.Compare(a.EndLine, b.EndLine), cmp.Compare(a.EndCol, b.EndCol))
}
