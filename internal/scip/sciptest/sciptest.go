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
