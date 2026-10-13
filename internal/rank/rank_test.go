package rank

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// id is the SCIP symbol of a function name in the test module.
func id(name string) string { return "scip-go gomod example.com/m v1 `example.com/m`/" + name + "()." }

// file is a document for build: the functions it defines, then the names it references, one
// occurrence each (a name twice is two references).
type file struct {
	path string
	defs []string
	refs []string
}

// build makes an index of files: each defined name is a global function with a definition there.
func build(files ...file) *model.Index {
	idx := &model.Index{}
	for _, f := range files {
		d := model.Document{Path: f.path}
		for i, name := range f.defs {
			r := model.Range{StartLine: i, EndLine: i, EndCol: len(name)}
			d.Occurrences = append(d.Occurrences, model.Occurrence{Range: r, Symbol: id(name), Roles: model.RoleDefinition})
			idx.Symbols = append(idx.Symbols, model.Symbol{
				ID: id(name), DisplayName: name, Kind: "Function",
				Definition: &model.Location{Path: f.path, Range: r},
			})
		}
		for i, name := range f.refs {
			r := model.Range{StartLine: 100 + i, EndLine: 100 + i, EndCol: len(name)}
			d.Occurrences = append(d.Occurrences, model.Occurrence{Range: r, Symbol: id(name), Roles: model.RoleReference | model.RoleRead})
		}
		idx.Documents = append(idx.Documents, d)
	}
	slices.SortFunc(idx.Symbols, func(a, b model.Symbol) int { return strings.Compare(a.ID, b.ID) })
	return idx
}

func paths(r Ranking) []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	return out
}

func names(f File) []string {
	var out []string
	for _, s := range f.Symbols {
		out = append(out, s.Name)
	}
	return out
}

func TestRankPutsTheMostReferencedFileFirst(t *testing.T) {
	idx := build(
		file{path: "leaf/c.go", defs: []string{"C"}, refs: []string{"Hub"}},
		file{path: "hub.go", defs: []string{"Hub"}},
		file{path: "leaf/a.go", defs: []string{"A"}, refs: []string{"Hub"}},
		file{path: "leaf/b.go", defs: []string{"B"}, refs: []string{"Hub"}},
	)
	r := Rank(idx, nil)
	// The leaves tie, so their paths order them.
	if got, want := paths(r), []string{"hub.go", "leaf/a.go", "leaf/b.go", "leaf/c.go"}; !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	sum := 0.0
	for _, f := range r.Files {
		sum += f.Rank
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("file ranks sum to %v, want 1", sum)
	}
	if r.Matched != 0 {
		t.Errorf("matched %d files without words", r.Matched)
	}
}

func TestRankDampsEdgesBySymbol(t *testing.T) {
	idx := build(
		file{path: "a.go", refs: []string{"B", "B", "B", "B", "B", "B", "B", "B", "B", "C1", "C2"}},
		file{path: "b.go", defs: []string{"B"}},
		file{path: "c.go", defs: []string{"C1", "C2"}},
	)
	g := newGraph(idx)
	// Nine references to one symbol weigh sqrt(9) = 3; one each to two symbols weigh 1 + 1.
	want := []Edge{{To: 1, Weight: 3}, {To: 2, Weight: 2}}
	if !reflect.DeepEqual(g.out[0], want) {
		t.Errorf("edges of a.go %v, want %v", g.out[0], want)
	}
}

func TestRankIgnoresSelfReferencesLocalsAndNamespaces(t *testing.T) {
	idx := build(
		file{path: "a.go", defs: []string{"A"}, refs: []string{"A", "A", "A"}},
		file{path: "b.go", defs: []string{"B"}},
	)
	pkg := "scip-go gomod example.com/m v1 `example.com/m`/"
	param := id("B") + "(x)"
	idx.Symbols = append(idx.Symbols,
		model.Symbol{ID: pkg, DisplayName: "m", Kind: "Package", Definition: &model.Location{Path: "b.go"}},
		model.Symbol{ID: param, DisplayName: "x", Kind: "Parameter", Definition: &model.Location{Path: "b.go"}},
		// A local that slipped into the globals is still a local.
		model.Symbol{ID: "local 3", DisplayName: "tmp", Definition: &model.Location{Path: "b.go"}},
	)
	slices.SortFunc(idx.Symbols, func(a, b model.Symbol) int { return strings.Compare(a.ID, b.ID) })
	idx.Documents[0].Occurrences = append(idx.Documents[0].Occurrences,
		model.Occurrence{Symbol: pkg, Roles: model.RoleReference},
		model.Occurrence{Symbol: param, Roles: model.RoleReference},
		model.Occurrence{Symbol: "local 3", Roles: model.RoleReference},
	)
	idx.Documents[0].Locals = []model.Symbol{{ID: "local 3", DisplayName: "tmp"}}

	if g := newGraph(idx); len(g.out[0]) != 0 || len(g.out[1]) != 0 {
		t.Fatalf("edges %v, want none", g.out)
	}
	r := Rank(idx, nil)
	if got, want := paths(r), []string{"a.go", "b.go"}; !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	if r.Files[0].Rank != r.Files[1].Rank {
		t.Errorf("unlinked files rank %v and %v, want a tie", r.Files[0].Rank, r.Files[1].Rank)
	}
	if got := names(r.Files[0]); !slices.Equal(got, []string{"A"}) || r.Files[0].Symbols[0].Refs != 0 {
		t.Errorf("a.go symbols %v (refs %d), want [A] with no references from other files", got, r.Files[0].Symbols[0].Refs)
	}
	if got := names(r.Files[1]); !slices.Equal(got, []string{"B"}) {
		t.Errorf("b.go symbols %v, want only [B]", got)
	}
}

func TestRankOrdersSymbolsByReferencesFromOtherFiles(t *testing.T) {
	idx := build(
		file{path: "core.go", defs: []string{"Zed", "Alpha", "Mid", "Beta"}, refs: []string{"Alpha", "Alpha"}},
		file{path: "use.go", refs: []string{"Mid", "Mid", "Mid", "Beta"}},
		file{path: "use2.go", refs: []string{"Beta"}},
	)
	r := Rank(idx, nil)
	if got := paths(r); !slices.Equal(got, []string{"core.go"}) {
		t.Fatalf("files %v, want only core.go: the others define nothing", got)
	}
	core := r.Files[0]
	// Mid 3 references, Beta 2, then Alpha and Zed with none from other files, by name.
	if got, want := names(core), []string{"Mid", "Beta", "Alpha", "Zed"}; !slices.Equal(got, want) {
		t.Fatalf("symbols %v, want %v", got, want)
	}
	// Scores share the file's rank by references + 1: 4, 3, 1 and 1 of 9.
	for i, w := range []float64{4, 3, 1, 1} {
		s := core.Symbols[i]
		if want := core.Rank * w / 9; math.Abs(s.Score-want) > 1e-12 {
			t.Errorf("%s score %v, want %v", s.Name, s.Score, want)
		}
	}
	if core.Symbols[0].Refs != 3 || core.Symbols[2].Refs != 0 {
		t.Errorf("refs %d and %d, want 3 and 0", core.Symbols[0].Refs, core.Symbols[2].Refs)
	}
}

func TestRankPersonalisesByPathAndSymbolName(t *testing.T) {
	idx := build(
		file{path: "app/main.go", defs: []string{"Main"}, refs: []string{"Serve"}},
		file{path: "app/server.go", defs: []string{"Serve"}, refs: []string{"Log"}},
		file{path: "app/log.go", defs: []string{"Log"}},
		file{path: "app/zeta.go", defs: []string{"ParseConfig"}},
	)
	plain := Rank(idx, nil)
	// Unpersonalised, the end of the chain leads and the isolated zeta.go comes last.
	if got, want := paths(plain), []string{"app/log.go", "app/server.go", "app/main.go", "app/zeta.go"}; !slices.Equal(got, want) {
		t.Fatalf("unpersonalised files %v, want %v", got, want)
	}
	for _, words := range [][]string{{"zeta"}, {"parse"}, Words("PARSECONFIG")} {
		r := Rank(idx, words)
		// The jump moves 15% of the rank, so zeta.go overtakes server.go but not log.go.
		if got, want := paths(r), []string{"app/log.go", "app/zeta.go", "app/server.go", "app/main.go"}; !slices.Equal(got, want) {
			t.Errorf("for %v: files %v, want %v", words, got, want)
		}
		if r.Files[1].Rank <= plain.Files[3].Rank {
			t.Errorf("for %v: zeta.go ranks %v, no more than its unpersonalised %v", words, r.Files[1].Rank, plain.Files[3].Rank)
		}
		if r.Matched != 1 {
			t.Errorf("for %v: matched %d files, want 1", words, r.Matched)
		}
	}
	// No file matching is the plain ranking, and says so.
	r := Rank(idx, []string{"nothing-like-it"})
	if r.Matched != 0 || !reflect.DeepEqual(r.Files, plain.Files) {
		t.Errorf("unmatched words: matched %d, files %v, want the plain ranking %v", r.Matched, r.Files, plain.Files)
	}
}

func TestRankNamesASymbolWithoutADisplayName(t *testing.T) {
	idx := &model.Index{
		Documents: []model.Document{{Path: "a.go"}},
		Symbols: []model.Symbol{
			{ID: "scip-go gomod m v1 `m`/Adder#Add.", Definition: &model.Location{Path: "a.go"}},
			{ID: "scip-go gomod m v1 `m`/Run().", Definition: &model.Location{Path: "a.go"}},
			{ID: "scip-go gomod m v1 `m`/T#Do(+1).", Definition: &model.Location{Path: "a.go"}},
			{ID: "scip-go gomod m v1 `m`/`odd name`#", Definition: &model.Location{Path: "a.go"}},
		},
	}
	r := Rank(idx, nil)
	if got, want := names(r.Files[0]), []string{"Add", "Do", "Run", "odd name"}; !slices.Equal(got, want) {
		t.Errorf("names %v, want %v", got, want)
	}
}

func TestRankOfAnEmptyIndexIsEmpty(t *testing.T) {
	for _, idx := range []*model.Index{{}, build(file{path: "a.go"}, file{path: "b.go"})} {
		if r := Rank(idx, []string{"a"}); len(r.Files) != 0 {
			t.Errorf("files %v, want none", r.Files)
		}
	}
}

func TestRankIsDeterministic(t *testing.T) {
	idx := synthetic(300, 8, 40)
	a, b := Rank(idx, Words("pkg3 Sym7")), Rank(idx, Words("pkg3 Sym7"))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two rankings of one index differ")
	}
}

func TestWordsSplitsAndLowers(t *testing.T) {
	if got, want := Words("  Parse, config\tPARSE  "), []string{"parse", "config"}; !slices.Equal(got, want) {
		t.Errorf("Words: %v, want %v", got, want)
	}
	if got := Words(" , "); got != nil {
		t.Errorf("Words of nothing: %v, want nil", got)
	}
}

// member adds a symbol of kind to the definition of the document at path, and one reference to it
// from the document at from.
func member(idx *model.Index, name, kind, path, from string) {
	memberID := "scip-go gomod example.com/m v1 `example.com/m`/T#" + name + "."
	idx.Symbols = append(idx.Symbols, model.Symbol{
		ID: memberID, DisplayName: name, Kind: kind, Definition: &model.Location{Path: path},
	})
	for i := range idx.Documents {
		if idx.Documents[i].Path == from {
			idx.Documents[i].Occurrences = append(idx.Documents[i].Occurrences,
				model.Occurrence{Symbol: memberID, Roles: model.RoleReference | model.RoleRead})
		}
	}
}

func TestRankLeavesFieldsAndEnumMembersOutOfTheMapButKeepsTheirEdges(t *testing.T) {
	for _, kind := range []string{"Field", "StaticField", "StaticDataMember", "EnumMember", "Property", "StaticProperty"} {
		t.Run(kind, func(t *testing.T) {
			idx := build(
				file{path: "types.go", defs: []string{"T"}},
				file{path: "use.go", refs: []string{"T"}},
				file{path: "fields.go"},
			)
			// use.go reads a member of types.go, and fields.go defines nothing but a member.
			member(idx, "F", kind, "types.go", "use.go")
			member(idx, "G", kind, "fields.go", "use.go")

			r := Rank(idx, nil)
			if got, want := paths(r), []string{"types.go"}; !slices.Equal(got, want) {
				t.Fatalf("files %v, want %v: fields.go defines only a %s", got, want, kind)
			}
			if got := names(r.Files[0]); !slices.Equal(got, []string{"T"}) {
				t.Errorf("types.go symbols %v, want [T] without the %s", got, kind)
			}
			// T's file takes the whole rank; the hidden member does not take a share of it.
			if s := r.Files[0].Symbols[0]; math.Abs(s.Score-r.Files[0].Rank) > 1e-12 {
				t.Errorf("T scores %v, want its file's whole rank %v", s.Score, r.Files[0].Rank)
			}
			// use.go reads T and F from types.go and G from fields.go: three symbols, two edges.
			g := newGraph(idx)
			if got := len(g.out[1]); got != 2 {
				t.Errorf("use.go has %d edges %v, want 2: members still link files", got, g.out[1])
			}
		})
	}
}
