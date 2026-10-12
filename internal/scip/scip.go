// Package scip reads a SCIP index into adamap's model. The index is untrusted input: its size is
// bounded, and a malformed range or path is an error, never a panic.
package scip

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	scippb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// DefaultMaxBytes is the default bound on the size of an index file: 1 GiB.
const DefaultMaxBytes int64 = 1 << 30

// ReadFile reads the SCIP index at path, refusing one larger than maxBytes, and converts it.
func ReadFile(path string, maxBytes int64) (*model.Index, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("the size limit must be positive, not %d", maxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tooBig := fmt.Errorf("%s is larger than the limit of %d bytes (--max-bytes)", path, maxBytes)
	if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() && fi.Size() > maxBytes {
		return nil, tooBig
	}
	// The limit holds while reading too, for a file that grows or is not a regular file.
	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, tooBig
	}
	idx, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return idx, nil
}

// Decode converts a serialized SCIP index.
func Decode(data []byte) (*model.Index, error) {
	var x scippb.Index
	if err := proto.Unmarshal(data, &x); err != nil {
		return nil, fmt.Errorf("not a SCIP index: %w", err)
	}
	return Convert(&x)
}

// document is a document being converted, with its locals by ID.
type document struct {
	model.Document
	locals map[string]*model.Symbol
}

// Convert turns a SCIP index into adamap's model. A document listed twice is merged; occurrences
// without a symbol (syntax highlighting only) are dropped; a symbol's definition is its first
// definition occurrence.
func Convert(x *scippb.Index) (*model.Index, error) {
	idx := &model.Index{
		Tool: model.Tool{
			Name:    x.GetMetadata().GetToolInfo().GetName(),
			Version: x.GetMetadata().GetToolInfo().GetVersion(),
		},
		ProjectRoot: x.GetMetadata().GetProjectRoot(),
	}
	globals := map[string]*model.Symbol{}
	// rels indexes each symbol's relationships by target, so combining them stays linear.
	rels := map[*model.Symbol]map[string]int{}
	var docs []*document
	byPath := map[string]*document{}
	for i, d := range x.GetDocuments() {
		path := d.GetRelativePath()
		if err := checkPath(path); err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
		doc := byPath[path]
		if doc == nil {
			doc = &document{Document: model.Document{Path: path}, locals: map[string]*model.Symbol{}}
			byPath[path] = doc
			docs = append(docs, doc)
		}
		if doc.Language == "" {
			doc.Language = d.GetLanguage()
		}
		for j, si := range d.GetSymbols() {
			if si.GetSymbol() == "" {
				return nil, fmt.Errorf("document %s: symbol information %d has no symbol", path, j)
			}
			if scippb.IsLocalSymbol(si.GetSymbol()) {
				merge(doc.locals, rels, si)
			} else {
				merge(globals, rels, si)
			}
		}
		for j, o := range d.GetOccurrences() {
			if o.GetSymbol() == "" {
				continue
			}
			occ, err := occurrence(o)
			if err != nil {
				return nil, fmt.Errorf("document %s: occurrence %d: %w", path, j, err)
			}
			doc.Occurrences = append(doc.Occurrences, occ)
			if occ.Roles.Has(model.RoleDefinition) {
				table := globals
				if scippb.IsLocalSymbol(occ.Symbol) {
					table = doc.locals
				}
				s := table[occ.Symbol]
				if s == nil {
					s = &model.Symbol{ID: occ.Symbol}
					table[occ.Symbol] = s
				}
				if s.Definition == nil {
					s.Definition = &model.Location{Path: path, Range: occ.Range}
				}
			}
		}
	}
	// External symbols are global by nature: a local one has no document to belong to.
	for _, si := range x.GetExternalSymbols() {
		if si.GetSymbol() != "" && !scippb.IsLocalSymbol(si.GetSymbol()) {
			merge(globals, rels, si)
		}
	}

	for _, doc := range docs {
		doc.Locals = sorted(doc.locals)
		slices.SortStableFunc(doc.Occurrences, func(a, b model.Occurrence) int {
			return cmp.Or(compareRange(a.Range, b.Range), strings.Compare(a.Symbol, b.Symbol))
		})
		idx.Documents = append(idx.Documents, doc.Document)
	}
	slices.SortFunc(idx.Documents, func(a, b model.Document) int { return strings.Compare(a.Path, b.Path) })
	idx.Symbols = sorted(globals)
	return idx, nil
}

// checkPath refuses a document path that could point outside the project: empty, absolute in any
// platform's spelling, or with a `..` segment.
func checkPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("no path")
	case strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.IsAbs(p) ||
		(len(p) >= 2 && p[1] == ':'):
		return fmt.Errorf("path %q is absolute", p)
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return fmt.Errorf("path %q has a .. segment", p)
		}
	}
	return nil
}

func occurrence(o *scippb.Occurrence) (model.Occurrence, error) {
	occ := model.Occurrence{Symbol: o.GetSymbol(), Roles: roles(o.GetSymbolRoles())}
	var err error
	if occ.Range, err = typedOrPacked(o.GetSingleLineRange(), o.GetMultiLineRange(), o.GetRange()); err != nil {
		return occ, err
	}
	single, multi, packed := o.GetSingleLineEnclosingRange(), o.GetMultiLineEnclosingRange(), o.GetEnclosingRange()
	if single != nil || multi != nil || len(packed) > 0 {
		r, err := typedOrPacked(single, multi, packed)
		if err != nil {
			return occ, fmt.Errorf("enclosing %w", err)
		}
		occ.Enclosing = &r
	}
	return occ, nil
}

// typedOrPacked reads a range from whichever encoding is set, the typed one first as SCIP says.
func typedOrPacked(single *scippb.SingleLineRange, multi *scippb.MultiLineRange, packed []int32) (model.Range, error) {
	switch {
	case single != nil:
		return toRange([]int32{single.Line, single.StartCharacter, single.EndCharacter})
	case multi != nil:
		return toRange([]int32{multi.StartLine, multi.StartCharacter, multi.EndLine, multi.EndCharacter})
	}
	return toRange(packed)
}

// toRange checks a SCIP range, [line, start, end] or [startLine, start, endLine, end].
func toRange(r []int32) (model.Range, error) {
	var sl, sc, el, ec int32
	switch len(r) {
	case 3:
		sl, sc, el, ec = r[0], r[1], r[0], r[2]
	case 4:
		sl, sc, el, ec = r[0], r[1], r[2], r[3]
	default:
		return model.Range{}, fmt.Errorf("range has %d ints, want 3 or 4", len(r))
	}
	switch {
	case sl < 0 || sc < 0 || el < 0 || ec < 0:
		return model.Range{}, fmt.Errorf("range %v is negative", r)
	case el < sl || (el == sl && ec < sc):
		return model.Range{}, fmt.Errorf("range %v ends before it starts", r)
	}
	return model.Range{StartLine: int(sl), StartCol: int(sc), EndLine: int(el), EndCol: int(ec)}, nil
}

func compareRange(a, b model.Range) int {
	return cmp.Or(cmp.Compare(a.StartLine, b.StartLine), cmp.Compare(a.StartCol, b.StartCol),
		cmp.Compare(a.EndLine, b.EndLine), cmp.Compare(a.EndCol, b.EndCol))
}

// roles maps SCIP's role bits; an occurrence that is not a definition is a reference.
func roles(bits int32) model.Role {
	r := model.RoleReference
	if bits&int32(scippb.SymbolRole_Definition) != 0 {
		r = model.RoleDefinition
	}
	if bits&int32(scippb.SymbolRole_Import) != 0 {
		r |= model.RoleImport
	}
	if bits&int32(scippb.SymbolRole_WriteAccess) != 0 {
		r |= model.RoleWrite
	}
	if bits&int32(scippb.SymbolRole_ReadAccess) != 0 {
		r |= model.RoleRead
	}
	return r
}

// merge adds si to table. When a symbol is described more than once, the first non-empty value of
// each field wins and the relationships are combined.
func merge(table map[string]*model.Symbol, rels map[*model.Symbol]map[string]int, si *scippb.SymbolInformation) {
	s := table[si.GetSymbol()]
	if s == nil {
		s = &model.Symbol{ID: si.GetSymbol()}
		table[s.ID] = s
	}
	if s.DisplayName == "" {
		s.DisplayName = si.GetDisplayName()
	}
	if s.Kind == "" && si.GetKind() != scippb.SymbolInformation_UnspecifiedKind {
		s.Kind = si.GetKind().String()
	}
	if s.Doc == "" {
		s.Doc = firstLine(si.GetDocumentation())
	}
	if s.Signature == "" {
		s.Signature = strings.TrimSpace(si.GetSignatureDocumentation().GetText())
	}
	for _, rel := range si.GetRelationships() {
		if rel.GetSymbol() == "" || !(rel.GetIsImplementation() || rel.GetIsReference() || rel.GetIsTypeDefinition()) {
			continue
		}
		if rels[s] == nil {
			rels[s] = map[string]int{}
		}
		i, ok := rels[s][rel.GetSymbol()]
		if !ok {
			s.Relationships = append(s.Relationships, model.Relationship{Symbol: rel.GetSymbol()})
			i = len(s.Relationships) - 1
			rels[s][rel.GetSymbol()] = i
		}
		r := &s.Relationships[i]
		r.Implementation = r.Implementation || rel.GetIsImplementation()
		r.Reference = r.Reference || rel.GetIsReference()
		r.TypeDefinition = r.TypeDefinition || rel.GetIsTypeDefinition()
	}
}

// firstLine is the first line of prose in a symbol's documentation, skipping fenced code (where
// indexers often put the signature).
func firstLine(docs []string) string {
	for _, d := range docs {
		fenced := false
		for line := range strings.Lines(d) {
			t := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(t, "```"):
				fenced = !fenced
			case !fenced && t != "":
				return t
			}
		}
	}
	return ""
}

// sorted returns the symbols in table by ID, each one's relationships by symbol.
func sorted(table map[string]*model.Symbol) []model.Symbol {
	var out []model.Symbol
	for _, s := range table {
		slices.SortFunc(s.Relationships, func(a, b model.Relationship) int { return strings.Compare(a.Symbol, b.Symbol) })
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b model.Symbol) int { return strings.Compare(a.ID, b.ID) })
	return out
}
