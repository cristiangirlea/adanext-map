// Package sciptest builds a small SCIP index in code for adamap's tests: no checked-in binary, no
// indexer.
package sciptest

import (
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// The fixture's global symbols.
const (
	Add      = "scip-go gomod example.com/m v1 `example.com/m/pkg`/Add()."
	Adder    = "scip-go gomod example.com/m v1 `example.com/m/pkg`/Adder#"
	AdderAdd = "scip-go gomod example.com/m v1 `example.com/m/pkg`/Adder#Add."
	Impl     = "scip-go gomod example.com/m v1 `example.com/m/pkg`/impl#"
	Fmt      = "scip-go gomod github.com/golang/go/src go1.26 fmt/"
)

// Index returns the fixture: two Go documents, pkg/a.go and pkg/b.go, defining Add (with an
// enclosing range and a signature), the interface Adder and impl, which implements it; each
// document has a `local 0` of its own, and b.go imports fmt, an external symbol. One occurrence
// uses a typed multi-line range, the rest the packed form.
func Index() *scippb.Index {
	def := int32(scippb.SymbolRole_Definition)
	return &scippb.Index{
		Metadata: &scippb.Metadata{
			Version:     scippb.ProtocolVersion_UnspecifiedProtocolVersion,
			ToolInfo:    &scippb.ToolInfo{Name: "scip-go", Version: "v0.2.7"},
			ProjectRoot: "file:///work/m",
		},
		Documents: []*scippb.Document{
			{
				RelativePath: "pkg/a.go",
				Language:     "go",
				Symbols: []*scippb.SymbolInformation{
					{
						Symbol:                 Add,
						DisplayName:            "Add",
						Kind:                   scippb.SymbolInformation_Function,
						Documentation:          []string{"```go\nfunc Add(a, b int) int\n```", "Add returns the sum.\nIt never fails."},
						SignatureDocumentation: &scippb.Signature{Language: "go", Text: "func Add(a, b int) int"},
					},
					{Symbol: Adder, DisplayName: "Adder", Kind: scippb.SymbolInformation_Interface},
					{Symbol: "local 0", DisplayName: "sum", Kind: scippb.SymbolInformation_Variable},
				},
				Occurrences: []*scippb.Occurrence{
					// Out of order on purpose: the converter sorts by range.
					{Range: []int32{7, 5, 10}, Symbol: Adder, SymbolRoles: def},
					{Range: []int32{2, 5, 8}, Symbol: Add, SymbolRoles: def, EnclosingRange: []int32{2, 0, 5, 1}},
					{Range: []int32{3, 1, 4}, Symbol: "local 0", SymbolRoles: def | int32(scippb.SymbolRole_WriteAccess)},
					{Range: []int32{4, 8, 11}, Symbol: "local 0", SymbolRoles: int32(scippb.SymbolRole_ReadAccess)},
					// A syntax-only occurrence carries no symbol and is dropped.
					{Range: []int32{0, 0, 7}, SyntaxKind: scippb.SyntaxKind_Keyword},
				},
			},
			{
				RelativePath: "pkg/b.go",
				Language:     "go",
				Symbols: []*scippb.SymbolInformation{
					{
						Symbol:        Impl,
						DisplayName:   "impl",
						Kind:          scippb.SymbolInformation_Struct,
						Relationships: []*scippb.Relationship{{Symbol: Adder, IsImplementation: true}},
					},
					{Symbol: "local 0", DisplayName: "x"},
				},
				Occurrences: []*scippb.Occurrence{
					{Range: []int32{2, 7, 12}, Symbol: Fmt, SymbolRoles: int32(scippb.SymbolRole_Import)},
					{Range: []int32{4, 5, 9}, Symbol: Impl, SymbolRoles: def},
					{
						TypedRange: &scippb.Occurrence_MultiLineRange{MultiLineRange: &scippb.MultiLineRange{
							StartLine: 6, StartCharacter: 9, EndLine: 7, EndCharacter: 2,
						}},
						Symbol: Add,
					},
					{Range: []int32{8, 1, 2}, Symbol: "local 0", SymbolRoles: def},
					// A method of Adder defined only by its occurrence, with no symbol information.
					{Range: []int32{9, 1, 4}, Symbol: AdderAdd, SymbolRoles: def},
				},
			},
		},
		ExternalSymbols: []*scippb.SymbolInformation{
			{Symbol: Fmt, DisplayName: "fmt", Kind: scippb.SymbolInformation_Package},
		},
	}
}

// Bytes marshals x, failing the test if it cannot.
func Bytes(t testing.TB, x *scippb.Index) []byte {
	t.Helper()
	data, err := proto.Marshal(x)
	if err != nil {
		t.Fatalf("marshal the SCIP fixture: %v", err)
	}
	return data
}

// The call-graph fixture's documents and global symbols. Remote is referenced but never described
// or defined anywhere in the index.
const (
	PathA     = "calls/a.go"
	PathB     = "calls/b.go"
	PathEmpty = "calls/empty.go"

	callsPkg = "scip-go gomod example.com/c v1 `example.com/c/calls`/"
	FnA      = callsPkg + "A()."
	FnB      = callsPkg + "B()."
	FnC      = callsPkg + "C()."
	FnD      = callsPkg + "D()."
	FnRec    = callsPkg + "Rec()."
	Thing    = callsPkg + "Thing#"
	ThingA   = callsPkg + "Thing#A()."
	FnRemote = "scip-go gomod example.com/other v1 `example.com/other`/Remote()."
)

// occ is an occurrence of sym on one line, columns c0 to c1, with an optional enclosing range.
func occ(line, c0, c1 int32, sym string, roles int32, enclosing ...int32) *scippb.Occurrence {
	o := &scippb.Occurrence{Range: []int32{line, c0, c1}, Symbol: sym, SymbolRoles: roles}
	if len(enclosing) > 0 {
		o.EnclosingRange = enclosing
	}
	return o
}

// CallGraph returns the fixture for the queries: three Go documents (lines are zero-based).
//
// calls/a.go: A calls B; B calls C and Remote; D calls A. A has an enclosing range; B and D have
// none, so a reference is placed in them by the span from their name to the next definition. B
// holds a local definition (local 1), which must not end that span.
//
// calls/b.go: C calls Rec; Rec calls itself and C, so C and Rec are a cycle. Thing is a struct and
// ThingA its method, with enclosing ranges nested one in the other: ThingA calls C, and Thing (past
// the method) calls Rec. At line 14 a reference to A sits outside every definition.
//
// calls/empty.go has no symbols. Remote has no SymbolInformation; ThingA is displayed as "A", the
// same name as the function A.
func CallGraph() *scippb.Index {
	def := int32(scippb.SymbolRole_Definition)
	fn := func(sym, name, sig string) *scippb.SymbolInformation {
		si := &scippb.SymbolInformation{Symbol: sym, DisplayName: name, Kind: scippb.SymbolInformation_Function}
		if sig != "" {
			si.SignatureDocumentation = &scippb.Signature{Language: "go", Text: sig}
		}
		return si
	}
	return &scippb.Index{
		Metadata: &scippb.Metadata{
			ToolInfo:    &scippb.ToolInfo{Name: "scip-go", Version: "v0.2.7"},
			ProjectRoot: "file:///work/c",
		},
		Documents: []*scippb.Document{
			{
				RelativePath: PathA,
				Language:     "go",
				Symbols: []*scippb.SymbolInformation{
					fn(FnA, "A", "func A()"),
					fn(FnB, "B", ""),
					fn(FnD, "D", ""),
					{Symbol: "local 0", DisplayName: "x", Kind: scippb.SymbolInformation_Variable},
					{Symbol: "local 1", DisplayName: "y", Kind: scippb.SymbolInformation_Variable},
				},
				Occurrences: []*scippb.Occurrence{
					occ(1, 5, 6, FnA, def, 1, 0, 5, 1),
					occ(2, 1, 2, "local 0", def),
					occ(3, 1, 2, FnB, 0),
					occ(4, 5, 6, "local 0", 0),
					occ(7, 5, 6, FnB, def),
					occ(8, 1, 2, "local 1", def),
					occ(9, 1, 2, FnC, 0),
					occ(10, 1, 7, FnRemote, 0),
					occ(13, 5, 6, FnD, def),
					occ(14, 1, 2, FnA, 0),
				},
			},
			{
				RelativePath: PathB,
				Language:     "go",
				Symbols: []*scippb.SymbolInformation{
					fn(FnC, "C", ""),
					fn(FnRec, "Rec", "func Rec()"),
					{Symbol: Thing, DisplayName: "Thing", Kind: scippb.SymbolInformation_Struct},
					{Symbol: ThingA, DisplayName: "A", Kind: scippb.SymbolInformation_Method},
				},
				Occurrences: []*scippb.Occurrence{
					occ(1, 5, 6, FnC, def, 1, 0, 5, 1),
					occ(3, 1, 4, FnRec, 0),
					occ(7, 5, 8, FnRec, def, 7, 0, 12, 1),
					occ(9, 1, 4, FnRec, 0),
					occ(10, 1, 2, FnC, 0),
					occ(14, 9, 10, FnA, 0),
					occ(16, 5, 10, Thing, def, 16, 0, 22, 1),
					occ(18, 10, 11, ThingA, def, 18, 0, 20, 1),
					occ(19, 1, 2, FnC, 0),
					occ(21, 1, 4, FnRec, 0),
				},
			},
			{RelativePath: PathEmpty, Language: "go"},
		},
	}
}
