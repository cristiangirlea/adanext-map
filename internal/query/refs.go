package query

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// Site is one occurrence of a symbol: where, and what kind of use. Kind is the first that applies
// of "definition", "import", "write", "read" and "reference".
type Site struct {
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Kind string `json:"kind"`
}

// FileRefs is the occurrences of a symbol in one file.
type FileRefs struct {
	Path string `json:"path"`
	Refs []Site `json:"refs"`
}

// RefsResult is every occurrence of a symbol. Total counts the occurrences in Files.
type RefsResult struct {
	Symbol Symbol     `json:"symbol"`
	Total  int        `json:"total"`
	Files  []FileRefs `json:"files"`
}

// siteKind names the use an occurrence with roles r makes of its symbol.
func siteKind(r model.Role) string {
	switch {
	case r.Has(model.RoleDefinition):
		return "definition"
	case r.Has(model.RoleImport):
		return "import"
	case r.Has(model.RoleWrite):
		return "write"
	case r.Has(model.RoleRead):
		return "read"
	}
	return "reference"
}

// site is the occurrence at r as a Site.
func (x *Index) site(r occRef) Site {
	o := &x.src.Documents[r.doc].Occurrences[r.occ]
	return Site{Line: o.Range.StartLine + 1, Col: o.Range.StartCol + 1, Kind: siteKind(o.Roles)}
}

// Refs lists every occurrence of the symbol id, definitions included, grouped by file. The files
// that define the symbol come first, then the rest, each by path; in a file the definitions come
// first, then the other occurrences, each by position. An id the index does not know is
// ErrNoSymbol.
func (x *Index) Refs(id string) (RefsResult, error) {
	e := x.byID[id]
	if e == nil {
		return RefsResult{}, fmt.Errorf("%w: %q", ErrNoSymbol, id)
	}
	res := RefsResult{Symbol: x.view(e), Files: []FileRefs{}}
	type file struct {
		path   string
		hasDef bool
		occs   []occRef
	}
	var files []*file
	for _, r := range x.occs[id] {
		d := &x.src.Documents[r.doc]
		if n := len(files); n == 0 || files[n-1].path != d.Path {
			files = append(files, &file{path: d.Path})
		}
		f := files[len(files)-1]
		f.occs = append(f.occs, r)
		f.hasDef = f.hasDef || d.Occurrences[r.occ].Roles.Has(model.RoleDefinition)
	}
	slices.SortStableFunc(files, func(a, b *file) int {
		return cmp.Or(-compareBool(a.hasDef, b.hasDef), strings.Compare(a.path, b.path))
	})
	for _, f := range files {
		occ := func(r occRef) *model.Occurrence { return &x.src.Documents[r.doc].Occurrences[r.occ] }
		slices.SortStableFunc(f.occs, func(a, b occRef) int {
			oa, ob := occ(a), occ(b)
			return cmp.Or(-compareBool(oa.Roles.Has(model.RoleDefinition), ob.Roles.Has(model.RoleDefinition)),
				compareRange(oa.Range, ob.Range))
		})
		fr := FileRefs{Path: f.path, Refs: make([]Site, 0, len(f.occs))}
		for _, r := range f.occs {
			fr.Refs = append(fr.Refs, x.site(r))
		}
		res.Total += len(fr.Refs)
		res.Files = append(res.Files, fr)
	}
	return res, nil
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	}
	return 1
}
