package scip

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	scippb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

func rng(sl, sc, el, ec int) model.Range {
	return model.Range{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
}

func at(path string, r model.Range) *model.Location { return &model.Location{Path: path, Range: r} }

func wantFixture() *model.Index {
	enclosing := rng(2, 0, 5, 1)
	return &model.Index{
		Tool:        model.Tool{Name: "scip-go", Version: "v0.2.7"},
		ProjectRoot: "file:///work/m",
		Documents: []model.Document{
			{
				Path:     "pkg/a.go",
				Language: "go",
				Locals: []model.Symbol{
					{ID: "local 0", DisplayName: "sum", Kind: "Variable", Definition: at("pkg/a.go", rng(3, 1, 3, 4))},
				},
				Occurrences: []model.Occurrence{
					{Range: rng(2, 5, 2, 8), Symbol: sciptest.Add, Roles: model.RoleDefinition, Enclosing: &enclosing},
					{Range: rng(3, 1, 3, 4), Symbol: "local 0", Roles: model.RoleDefinition | model.RoleWrite},
					{Range: rng(4, 8, 4, 11), Symbol: "local 0", Roles: model.RoleReference | model.RoleRead},
					{Range: rng(7, 5, 7, 10), Symbol: sciptest.Adder, Roles: model.RoleDefinition},
				},
			},
			{
				Path:     "pkg/b.go",
				Language: "go",
				Locals: []model.Symbol{
					{ID: "local 0", DisplayName: "x", Definition: at("pkg/b.go", rng(8, 1, 8, 2))},
				},
				Occurrences: []model.Occurrence{
					{Range: rng(2, 7, 2, 12), Symbol: sciptest.Fmt, Roles: model.RoleReference | model.RoleImport},
					{Range: rng(4, 5, 4, 9), Symbol: sciptest.Impl, Roles: model.RoleDefinition},
					{Range: rng(6, 9, 7, 2), Symbol: sciptest.Add, Roles: model.RoleReference},
					{Range: rng(8, 1, 8, 2), Symbol: "local 0", Roles: model.RoleDefinition},
					{Range: rng(9, 1, 9, 4), Symbol: sciptest.AdderAdd, Roles: model.RoleDefinition},
				},
			},
		},
		Symbols: []model.Symbol{
			{
				ID: sciptest.Add, DisplayName: "Add", Kind: "Function",
				Doc: "Add returns the sum.", Signature: "func Add(a, b int) int",
				Definition: at("pkg/a.go", rng(2, 5, 2, 8)),
			},
			{ID: sciptest.Adder, DisplayName: "Adder", Kind: "Interface", Definition: at("pkg/a.go", rng(7, 5, 7, 10))},
			{ID: sciptest.AdderAdd, Definition: at("pkg/b.go", rng(9, 1, 9, 4))},
			{
				ID: sciptest.Impl, DisplayName: "impl", Kind: "Struct", Definition: at("pkg/b.go", rng(4, 5, 4, 9)),
				Relationships: []model.Relationship{{Symbol: sciptest.Adder, Implementation: true}},
			},
			{ID: sciptest.Fmt, DisplayName: "fmt", Kind: "Package"},
		},
	}
}

// diff renders both sides as JSON, which names the field that differs.
func diff(t *testing.T, got, want *model.Index) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	g, _ := json.MarshalIndent(got, "", "  ")
	w, _ := json.MarshalIndent(want, "", "  ")
	t.Fatalf("model differs\ngot:\n%s\nwant:\n%s", g, w)
}

func TestConvertBuildsTheModel(t *testing.T) {
	got, err := Convert(sciptest.Index())
	if err != nil {
		t.Fatal(err)
	}
	diff(t, got, wantFixture())
}

func TestDecodeReadsTheSerializedIndex(t *testing.T) {
	got, err := Decode(sciptest.Bytes(t, sciptest.Index()))
	if err != nil {
		t.Fatal(err)
	}
	diff(t, got, wantFixture())
}

func TestDecodeRefusesBytesThatAreNotAnIndex(t *testing.T) {
	_, err := Decode([]byte{0xff, 0xff, 0xff})
	if err == nil || !strings.Contains(err.Error(), "not a SCIP index") {
		t.Fatalf("err = %v, want one saying it is not a SCIP index", err)
	}
}

func TestConvertMergesADocumentListedTwice(t *testing.T) {
	x := sciptest.Index()
	again := &scippb.Document{
		RelativePath: "pkg/a.go",
		Occurrences:  []*scippb.Occurrence{{Range: []int32{9, 0, 3}, Symbol: sciptest.Impl}},
	}
	x.Documents = append(x.Documents, again)
	got, err := Convert(x)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Documents) != 2 {
		t.Fatalf("%d documents, want the two paths once each", len(got.Documents))
	}
	occs := got.Documents[0].Occurrences
	if last := occs[len(occs)-1]; last.Symbol != sciptest.Impl || last.Range != rng(9, 0, 9, 3) {
		t.Fatalf("a.go's last occurrence is %+v, want the merged reference to impl", last)
	}
}

func TestConvertReadsTypedRangesBeforePackedOnes(t *testing.T) {
	x := sciptest.Index()
	x.Documents[1].Occurrences = []*scippb.Occurrence{
		{
			Symbol:              sciptest.Impl,
			TypedRange:          &scippb.Occurrence_SingleLineRange{SingleLineRange: &scippb.SingleLineRange{Line: 9, StartCharacter: 2, EndCharacter: 5}},
			TypedEnclosingRange: &scippb.Occurrence_SingleLineEnclosingRange{SingleLineEnclosingRange: &scippb.SingleLineRange{Line: 9, StartCharacter: 0, EndCharacter: 7}},
		},
		{
			Symbol: sciptest.Adder,
			// Both encodings set: the typed one wins, as SCIP says.
			Range:          []int32{1, 1, 2},
			TypedRange:     &scippb.Occurrence_SingleLineRange{SingleLineRange: &scippb.SingleLineRange{Line: 11, StartCharacter: 3, EndCharacter: 6}},
			EnclosingRange: []int32{1, 0, 3},
			TypedEnclosingRange: &scippb.Occurrence_MultiLineEnclosingRange{MultiLineEnclosingRange: &scippb.MultiLineRange{
				StartLine: 10, StartCharacter: 1, EndLine: 12, EndCharacter: 4,
			}},
		},
	}
	got, err := Convert(x)
	if err != nil {
		t.Fatal(err)
	}
	enc1, enc2 := rng(9, 0, 9, 7), rng(10, 1, 12, 4)
	want := []model.Occurrence{
		{Range: rng(9, 2, 9, 5), Symbol: sciptest.Impl, Roles: model.RoleReference, Enclosing: &enc1},
		{Range: rng(11, 3, 11, 6), Symbol: sciptest.Adder, Roles: model.RoleReference, Enclosing: &enc2},
	}
	if occs := got.Documents[1].Occurrences; !reflect.DeepEqual(occs, want) {
		t.Fatalf("occurrences = %+v, want %+v", occs, want)
	}
}

func TestConvertCombinesSymbolDescriptions(t *testing.T) {
	x := sciptest.Index()
	// impl described again after its first description: the first non-empty value of each field
	// wins, and the relationships combine; one with no kind set is dropped.
	x.Documents[1].Symbols = append(x.Documents[1].Symbols, &scippb.SymbolInformation{
		Symbol:        sciptest.Impl,
		DisplayName:   "other",
		Kind:          scippb.SymbolInformation_Class,
		Documentation: []string{"impl adds."},
		Relationships: []*scippb.Relationship{
			{Symbol: sciptest.Adder, IsReference: true},
			{Symbol: sciptest.Fmt, IsTypeDefinition: true},
			{Symbol: sciptest.Add},
		},
	})
	// An external local symbol has no document to belong to and is dropped.
	x.ExternalSymbols = append(x.ExternalSymbols, &scippb.SymbolInformation{Symbol: "local 5"})
	got, err := Convert(x)
	if err != nil {
		t.Fatal(err)
	}
	var impl model.Symbol
	for _, s := range got.Symbols {
		if s.ID == "local 5" {
			t.Errorf("the external local symbol was kept")
		}
		if s.ID == sciptest.Impl {
			impl = s
		}
	}
	want := model.Symbol{
		ID: sciptest.Impl, DisplayName: "impl", Kind: "Struct", Doc: "impl adds.",
		Definition: at("pkg/b.go", rng(4, 5, 4, 9)),
		Relationships: []model.Relationship{
			{Symbol: sciptest.Adder, Implementation: true, Reference: true},
			{Symbol: sciptest.Fmt, TypeDefinition: true},
		},
	}
	if !reflect.DeepEqual(impl, want) {
		t.Fatalf("impl = %+v, want %+v", impl, want)
	}
}

func TestConvertRoles(t *testing.T) {
	x := sciptest.Index()
	x.Documents[1].Occurrences = []*scippb.Occurrence{
		// A second definition of Add: the first one, in a.go, stays its definition.
		{Range: []int32{1, 0, 3}, Symbol: sciptest.Add, SymbolRoles: int32(scippb.SymbolRole_Definition)},
		// A forward definition is a declaration, not the definition.
		{Range: []int32{2, 0, 3}, Symbol: sciptest.Impl, SymbolRoles: int32(scippb.SymbolRole_ForwardDefinition)},
	}
	got, err := Convert(x)
	if err != nil {
		t.Fatal(err)
	}
	if def := got.Symbols[0].Definition; def == nil || def.Path != "pkg/a.go" {
		t.Errorf("Add's definition = %+v, want the first, in pkg/a.go", def)
	}
	if roles := got.Documents[1].Occurrences[1].Roles; roles != model.RoleReference {
		t.Errorf("the forward definition's roles = %b, want reference only", roles)
	}
	if got.Symbols[3].Definition != nil {
		t.Errorf("impl has a definition from a forward definition: %+v", got.Symbols[3].Definition)
	}
}

func TestConvertRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(x *scippb.Index)
		want   string
	}{
		{"range of two ints", func(x *scippb.Index) { x.Documents[0].Occurrences[0].Range = []int32{1, 2} }, "3 or 4"},
		{"range of five ints", func(x *scippb.Index) { x.Documents[0].Occurrences[0].Range = []int32{1, 2, 3, 4, 5} }, "3 or 4"},
		{"no range", func(x *scippb.Index) { x.Documents[0].Occurrences[0].Range = nil }, "3 or 4"},
		{"negative range", func(x *scippb.Index) { x.Documents[0].Occurrences[0].Range = []int32{-1, 0, 3} }, "negative"},
		{"inverted range on a line", func(x *scippb.Index) { x.Documents[0].Occurrences[0].Range = []int32{5, 4, 2} }, "ends before"},
		{"inverted range over lines", func(x *scippb.Index) { x.Documents[0].Occurrences[0].Range = []int32{5, 0, 4, 9} }, "ends before"},
		{"inverted typed range", func(x *scippb.Index) {
			x.Documents[1].Occurrences[2].TypedRange = &scippb.Occurrence_MultiLineRange{MultiLineRange: &scippb.MultiLineRange{
				StartLine: 7, EndLine: 6,
			}}
		}, "ends before"},
		{"malformed enclosing range", func(x *scippb.Index) { x.Documents[0].Occurrences[1].EnclosingRange = []int32{1} }, "enclosing"},
		{"inverted typed enclosing range", func(x *scippb.Index) {
			x.Documents[0].Occurrences[1].TypedEnclosingRange = &scippb.Occurrence_SingleLineEnclosingRange{
				SingleLineEnclosingRange: &scippb.SingleLineRange{Line: 2, StartCharacter: 5, EndCharacter: 1},
			}
		}, "enclosing"},
		{"absolute path", func(x *scippb.Index) { x.Documents[0].RelativePath = "/etc/passwd" }, "absolute"},
		{"drive path", func(x *scippb.Index) { x.Documents[0].RelativePath = `C:\Windows\x.go` }, "absolute"},
		{"UNC path", func(x *scippb.Index) { x.Documents[0].RelativePath = `\\server\share\x.go` }, "absolute"},
		{"dot-dot path", func(x *scippb.Index) { x.Documents[0].RelativePath = "../x.go" }, ".."},
		{"dot-dot inside the path", func(x *scippb.Index) { x.Documents[0].RelativePath = "pkg/../../x.go" }, ".."},
		{"dot-dot with backslashes", func(x *scippb.Index) { x.Documents[0].RelativePath = `pkg\..\..\x.go` }, ".."},
		{"empty path", func(x *scippb.Index) { x.Documents[0].RelativePath = "" }, "no path"},
		{"symbol information without a symbol", func(x *scippb.Index) { x.Documents[0].Symbols[0].Symbol = "" }, "no symbol"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := sciptest.Index()
			c.break_(x)
			got, err := Convert(x)
			if err == nil {
				t.Fatalf("no error; model %+v", got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// A symbol with many relationships costs time in proportion to their number: with a search per
// relationship, 200,000 of them took minutes.
func TestConvertTakesLinearTimeInRelationships(t *testing.T) {
	x := sciptest.Index()
	si := x.Documents[1].Symbols[0]
	for i := range 200_000 {
		si.Relationships = append(si.Relationships, &scippb.Relationship{Symbol: fmt.Sprint("s", i), IsReference: true})
	}
	start := time.Now()
	got, err := Convert(x)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("Convert took %s", took)
	}
	if n := len(got.Symbols[3].Relationships); n != 200_001 {
		t.Fatalf("impl has %d relationships, want 200001", n)
	}
}

func TestReadFileBoundsTheSize(t *testing.T) {
	data := sciptest.Bytes(t, sciptest.Index())
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path, int64(len(data)))
	if err != nil {
		t.Fatalf("at exactly the limit: %v", err)
	}
	diff(t, got, wantFixture())

	_, err = ReadFile(path, int64(len(data))-1)
	if err == nil || !strings.Contains(err.Error(), "--max-bytes") {
		t.Fatalf("one byte over the limit: err = %v, want one naming --max-bytes", err)
	}
}

func TestReadFileRefusesANonPositiveLimit(t *testing.T) {
	if _, err := ReadFile("index.scip", 0); err == nil {
		t.Fatal("a limit of 0 was accepted")
	}
}
