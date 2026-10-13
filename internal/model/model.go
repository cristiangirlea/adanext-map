// Package model is adamap's own view of a code index: what the store holds and the queries read.
// It does not depend on SCIP, so the store's format stays adamap's to keep: fields are only ever
// added, with omitempty, and the Role values are never renumbered.
package model

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Index is one indexed tree at one commit.
type Index struct {
	Tool        Tool       `json:"tool"`
	ProjectRoot string     `json:"project_root,omitempty"`
	Documents   []Document `json:"documents,omitempty"`
	// Symbols are the global symbols sorted by ID: those the documents define or describe, and the
	// external ones the indexer described. Local symbols live in their document.
	Symbols []Symbol `json:"symbols,omitempty"`
}

// Tool is the indexer that produced the index.
type Tool struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// Document is one source file, its path relative to the project root as the indexer gave it
// (SCIP asks for forward slashes).
type Document struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	// Locals are the document's local symbols (`local N` in SCIP), sorted by ID; the same ID in
	// another document is another symbol.
	Locals []Symbol `json:"locals,omitempty"`
	// Occurrences are sorted by range, then symbol.
	Occurrences []Occurrence `json:"occurrences,omitempty"`
}

// Symbol is a named thing in the code, identified by its SCIP symbol string.
type Symbol struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	// Kind is the SCIP kind's name ("Function", "Struct"), empty when the indexer gave none.
	Kind string `json:"kind,omitempty"`
	// Doc is the first line of prose in the documentation, code fences skipped.
	Doc       string `json:"doc,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Definition is where the symbol is defined; nil for a symbol defined outside the index.
	Definition    *Location      `json:"definition,omitempty"`
	Relationships []Relationship `json:"relationships,omitempty"`
}

// Location is a range in a document.
type Location struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
}

// Range is a span of a document: zero-based lines and columns, the end exclusive. Columns are in
// the indexer's position encoding.
type Range struct {
	StartLine int `json:"start_line"`
	StartCol  int `json:"start_col"`
	EndLine   int `json:"end_line"`
	EndCol    int `json:"end_col"`
}

// Occurrence is one appearance of a symbol in a document.
type Occurrence struct {
	Range  Range  `json:"range"`
	Symbol string `json:"symbol"`
	Roles  Role   `json:"roles"`
	// Enclosing is the range of the whole construct around the occurrence, when the indexer gives
	// one: the function body around a definition, or the call around a reference.
	Enclosing *Range `json:"enclosing,omitempty"`
}

// Role is a set of the roles an occurrence plays. The values are stored: never renumber them.
type Role uint8

const (
	RoleDefinition Role = 1 << iota
	// RoleReference is set on every occurrence that is not a definition.
	RoleReference
	RoleImport
	RoleWrite
	RoleRead
)

// Has reports whether r includes every role in o.
func (r Role) Has(o Role) bool { return r&o == o }

// Relationship links a symbol to another: an interface it implements, a symbol it refers to as
// part of itself, or its type.
type Relationship struct {
	Symbol         string `json:"symbol"`
	Implementation bool   `json:"implementation,omitempty"`
	Reference      bool   `json:"reference,omitempty"`
	TypeDefinition bool   `json:"type_definition,omitempty"`
}

// Counts are the sizes of an index: documents, symbols global and local, and occurrences.
type Counts struct {
	Documents   int `json:"documents"`
	Symbols     int `json:"symbols"`
	Occurrences int `json:"occurrences"`
}

// Counts counts x's documents, symbols (global and local) and occurrences.
func (x *Index) Counts() Counts {
	c := Counts{Documents: len(x.Documents), Symbols: len(x.Symbols)}
	for _, d := range x.Documents {
		c.Symbols += len(d.Locals)
		c.Occurrences += len(d.Occurrences)
	}
	return c
}

// CheckPath refuses a document path that could point outside the project: empty, absolute in any
// platform's spelling, or with a `..` segment.
func CheckPath(p string) error {
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

// Limits bound how many items an index may hold. The bytes of a file or a stream do not bound the
// memory it takes: a few bytes of JSON or protobuf (`{},`) become a whole struct once decoded.
type Limits struct {
	Documents     int
	Symbols       int // global and local, as in Counts
	Occurrences   int
	Relationships int // over all symbols
}

// DefaultLimits are generous: a Rust workspace of 2,000 files measured about 20,000 occurrences per
// 17 files, 2.4 million in all, so 50 million occurrences is room for some 40,000 such files.
func DefaultLimits() Limits {
	return Limits{Documents: 2_000_000, Symbols: 20_000_000, Occurrences: 50_000_000, Relationships: 20_000_000}
}

// Check returns an error naming the first of c's counts, or of the relationship count rels, that is
// over its limit.
func (l Limits) Check(c Counts, rels int) error {
	switch {
	case c.Documents > l.Documents:
		return ErrOverLimit("documents", l.Documents)
	case c.Symbols > l.Symbols:
		return ErrOverLimit("symbols", l.Symbols)
	case c.Occurrences > l.Occurrences:
		return ErrOverLimit("occurrences", l.Occurrences)
	case rels > l.Relationships:
		return ErrOverLimit("relationships", l.Relationships)
	}
	return nil
}

// ErrOverLimit is the error for more than max of what.
func ErrOverLimit(what string, max int) error {
	return fmt.Errorf("more than the limit of %d %s", max, what)
}
