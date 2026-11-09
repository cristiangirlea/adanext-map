// Package scip reads a SCIP index into adamap's model. The index is untrusted input: its size is
// bounded, and a malformed range or path is an error, never a panic.
package scip

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	scippb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// DefaultMaxBytes is the default bound on the size of an index file: 1 GiB.
const DefaultMaxBytes int64 = 1 << 30

// ReadFile reads the SCIP index at path, refusing one larger than maxBytes, and converts it.
func ReadFile(path string, maxBytes int64) (*model.Index, Skipped, error) {
	if maxBytes <= 0 {
		return nil, Skipped{}, fmt.Errorf("the size limit must be positive, not %d", maxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Skipped{}, err
	}
	defer f.Close()
	tooBig := fmt.Errorf("%s is larger than the limit of %d bytes (--max-bytes)", path, maxBytes)
	if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() && fi.Size() > maxBytes {
		return nil, Skipped{}, tooBig
	}
	// The limit holds while reading too, for a file that grows or is not a regular file.
	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, Skipped{}, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, Skipped{}, tooBig
	}
	idx, skipped, err := Decode(data)
	if err != nil {
		return nil, Skipped{}, fmt.Errorf("%s: %w", path, err)
	}
	return idx, skipped, nil
}

// Decode converts a serialized SCIP index. The bytes are bounded by the caller (ReadFile's maxBytes);
// the counts of documents, symbols, occurrences and relationships by model.DefaultLimits.
func Decode(data []byte) (*model.Index, Skipped, error) {
	return decode(data, model.DefaultLimits())
}

// decode converts each document before decoding the next, keeping protobuf messages short-lived.
func decode(data []byte, lim model.Limits) (*model.Index, Skipped, error) {
	c := newConverter(model.CheckPath)
	var counts model.Counts
	relationships := 0
	var metadata scippb.Metadata
	check := func(symbols []*scippb.SymbolInformation) error {
		counts.Symbols += len(symbols)
		for _, si := range symbols {
			relationships += len(si.GetRelationships())
		}
		if err := lim.Check(counts, relationships); err != nil {
			return fmt.Errorf("the SCIP index has %w", err)
		}
		return nil
	}
	var visitorErr error
	visitor := scippb.IndexVisitor{
		VisitDocument: func(_ context.Context, d *scippb.Document) error {
			i := counts.Documents
			counts.Documents++
			counts.Occurrences += len(d.GetOccurrences())
			visitorErr = check(d.GetSymbols())
			if visitorErr == nil {
				visitorErr = c.addDocument(i, d)
			}
			return visitorErr
		},
		VisitExternalSymbol: func(_ context.Context, si *scippb.SymbolInformation) error {
			visitorErr = check([]*scippb.SymbolInformation{si})
			if visitorErr != nil {
				return visitorErr
			}
			c.addExternal(si)
			return nil
		},
	}
	// External descriptions follow all documents so the first document description still wins.
	for _, external := range []bool{false, true} {
		for rest := data; len(rest) > 0; {
			num, typ, tagLen := protowire.ConsumeTag(rest)
			if tagLen < 0 {
				return nil, Skipped{}, fmt.Errorf("not a SCIP index: %w", protowire.ParseError(tagLen))
			}
			if !num.IsValid() {
				return nil, Skipped{}, fmt.Errorf("not a SCIP index: invalid field number %d", num)
			}
			n := protowire.ConsumeFieldValue(num, typ, rest[tagLen:])
			if n < 0 {
				return nil, Skipped{}, fmt.Errorf("not a SCIP index: %w", protowire.ParseError(n))
			}
			field, tail := rest[:tagLen+n], rest[tagLen+n:]
			// Validate lengths before the bindings allocate, and skip unknown fields as proto.Unmarshal did.
			if typ == protowire.BytesType && num == 1 && !external {
				payload, _ := protowire.ConsumeBytes(field[tagLen:])
				// Merging raw metadata preserves explicitly empty scalars in repeated messages.
				if err := (proto.UnmarshalOptions{Merge: true}).Unmarshal(payload, &metadata); err != nil {
					return nil, Skipped{}, fmt.Errorf("not a SCIP index: %w", err)
				}
			}
			if typ == protowire.BytesType && num >= 2 && num <= 3 && (num == 3) == external {
				// The bindings require single-byte tags, so canonicalize valid overlong encodings.
				reader := io.MultiReader(bytes.NewReader([]byte{byte(num<<3) | byte(protowire.BytesType)}), bytes.NewReader(field[tagLen:]))
				if err := visitor.ParseStreaming(context.Background(), reader); err != nil {
					// Visitor refusals describe valid protobuf that cannot be converted.
					if visitorErr != nil {
						return nil, Skipped{}, visitorErr
					}
					return nil, Skipped{}, fmt.Errorf("not a SCIP index: %w", err)
				}
			}
			rest = tail
		}
	}
	c.idx.Tool = model.Tool{Name: metadata.GetToolInfo().GetName(), Version: metadata.GetToolInfo().GetVersion()}
	c.idx.ProjectRoot = metadata.GetProjectRoot()
	idx, skipped := c.finish()
	// Definitions without SymbolInformation can increase the converted symbol count.
	if err := lim.Check(idx.Counts(), 0); err != nil {
		return nil, Skipped{}, fmt.Errorf("the converted SCIP index has %w", err)
	}
	return idx, skipped, nil
}

// maxSkippedPaths is how many skipped paths a Skipped keeps for a message.
const maxSkippedPaths = 3

// Skipped counts documents outside the project, naming its root, without a path or with unsafe names,
// and keeps the first few paths, quoted and bounded to 200 bytes each, for a message.
type Skipped struct {
	Count int
	Paths []string
}

func (s *Skipped) add(p string) {
	s.Count++
	if len(s.Paths) < maxSkippedPaths {
		// Bound the quoted form too, since escaping can expand a hostile path.
		const maxQuotedBytes = 200
		end := 0
		for i := range p {
			if len(strconv.Quote(p[:i]+"...")) > maxQuotedBytes {
				break
			}
			end = i
			if i >= maxQuotedBytes {
				break
			}
		}
		if len(p) > maxQuotedBytes || len(strconv.Quote(p)) > maxQuotedBytes {
			p = p[:end] + "..."
		}
		s.Paths = append(s.Paths, strconv.Quote(p))
	}
}

// document is a document being converted, with its locals by ID.
type document struct {
	model.Document
	originalPath string
	locals       map[string]*model.Symbol
}

// Convert turns a SCIP index into adamap's model. Exact duplicate paths merge; aliases fail. Occurrences
// without a symbol (syntax highlighting only) are dropped; a symbol's definition is its first
// definition occurrence. A document whose path leaves the project or is empty is skipped with its
// occurrences and symbols, as is a known unsafe file name; any other problem is an error.
func Convert(x *scippb.Index) (*model.Index, Skipped, error) {
	return convert(x, model.CheckPath)
}

func convert(x *scippb.Index, checkPath func(string) error) (*model.Index, Skipped, error) {
	c := newConverter(checkPath)
	c.idx.Tool = model.Tool{Name: x.GetMetadata().GetToolInfo().GetName(), Version: x.GetMetadata().GetToolInfo().GetVersion()}
	c.idx.ProjectRoot = x.GetMetadata().GetProjectRoot()
	for i, d := range x.GetDocuments() {
		if err := c.addDocument(i, d); err != nil {
			return nil, Skipped{}, err
		}
	}
	for _, si := range x.GetExternalSymbols() {
		c.addExternal(si)
	}
	idx, skipped := c.finish()
	return idx, skipped, nil
}

type converter struct {
	idx     *model.Index
	skipped Skipped
	globals map[string]*model.Symbol
	rels    map[*model.Symbol]map[string]int // Relationship offsets keep merging linear.
	docs    []*document
	byPath  map[string]*document
	// checkPath validates a kept document's path; tests replace it.
	checkPath func(string) error
}

func newConverter(checkPath func(string) error) *converter {
	return &converter{idx: &model.Index{}, globals: map[string]*model.Symbol{}, rels: map[*model.Symbol]map[string]int{}, byPath: map[string]*document{}, checkPath: checkPath}
}

func (c *converter) addDocument(i int, d *scippb.Document) error {
	path := normalisePath(d.GetRelativePath())
	if outsideProject(path) {
		c.skipped.add(d.GetRelativePath())
		return nil
	}
	if err := c.checkPath(path); err != nil {
		if errors.Is(err, model.ErrUnsafeName) {
			c.skipped.add(d.GetRelativePath())
			return nil
		}
		return fmt.Errorf("document %d: %w", i, err)
	}
	doc := c.byPath[path]
	if doc == nil {
		doc = &document{Document: model.Document{Path: path}, originalPath: d.GetRelativePath(), locals: map[string]*model.Symbol{}}
		c.byPath[path] = doc
		c.docs = append(c.docs, doc)
	}
	if doc.originalPath != d.GetRelativePath() {
		return fmt.Errorf("document %d: different spellings %q and %q name the same path %q", i, doc.originalPath, d.GetRelativePath(), path)
	}
	if doc.Language == "" {
		doc.Language = d.GetLanguage()
	}
	for j, si := range d.GetSymbols() {
		if si.GetSymbol() == "" {
			return fmt.Errorf("document %q: symbol information %d has no symbol", path, j)
		}
		if scippb.IsLocalSymbol(si.GetSymbol()) {
			merge(doc.locals, c.rels, si)
		} else {
			merge(c.globals, c.rels, si)
		}
	}
	for j, o := range d.GetOccurrences() {
		if o.GetSymbol() == "" {
			continue
		}
		occ, err := occurrence(o)
		if err != nil {
			return fmt.Errorf("document %q: occurrence %d: %w", path, j, err)
		}
		doc.Occurrences = append(doc.Occurrences, occ)
		if occ.Roles.Has(model.RoleDefinition) {
			table := c.globals
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
	return nil
}

func (c *converter) addExternal(si *scippb.SymbolInformation) {
	// External local symbols have no document to belong to.
	if si.GetSymbol() != "" && !scippb.IsLocalSymbol(si.GetSymbol()) {
		merge(c.globals, c.rels, si)
	}
}

func (c *converter) finish() (*model.Index, Skipped) {
	for _, doc := range c.docs {
		doc.Locals = sorted(doc.locals)
		slices.SortStableFunc(doc.Occurrences, func(a, b model.Occurrence) int {
			return cmp.Or(compareRange(a.Range, b.Range), strings.Compare(a.Symbol, b.Symbol))
		})
		c.idx.Documents = append(c.idx.Documents, doc.Document)
	}
	slices.SortFunc(c.idx.Documents, func(a, b model.Document) int { return strings.Compare(a.Path, b.Path) })
	c.idx.Symbols = sorted(c.globals)
	return c.idx, c.skipped
}

// normalisePath is a document path in slash form, cleaned. SCIP specifies "/", but indexers on
// Windows write a backslash, which is always taken as a separator here. An empty path stays empty,
// so it is not taken for the project root.
func normalisePath(p string) string {
	if p == "" {
		return p
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// Only paths outside the project, naming its root or empty bypass file-name validation. An empty
// path is what an indexer writes for a file it cannot place under the root, such as one on another drive.
func outsideProject(p string) bool {
	return p == "" || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") ||
		(len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && p[2] == '/')
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
