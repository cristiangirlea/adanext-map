// Package rank orders an index's files and symbols for the repo map: files by PageRank over the
// references between them, a file's symbols by how often other files use them. render.go fits the
// result into a token budget.
package rank

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// Ranking is an index's files, most important first.
type Ranking struct {
	// Files are the files that define at least one symbol, by rank, then path.
	Files []File
	// Matched is how many files the words matched; 0 means the ranking is not personalised.
	Matched int
}

// File is a file and the symbols it defines.
type File struct {
	Path string
	// Rank is the file's PageRank; all files' ranks sum to 1.
	Rank float64
	// Symbols are by references from other files, most first, then name, then ID.
	Symbols []Symbol
}

// Symbol is a symbol a file defines.
type Symbol struct {
	ID string
	// Name is the display name, or the last name in the ID when the indexer gave none.
	Name      string
	Kind      string
	Signature string
	Doc       string
	// Refs counts the references to the symbol from other files.
	Refs int
	// Score is the file's rank shared among its symbols in proportion to Refs + 1.
	Score float64
}

// def is a symbol the map can show, and the file that defines it.
type def struct {
	sym  *model.Symbol
	file int
	name string
}

// graph is the reference graph between an index's documents.
type graph struct {
	defs []def
	// byFile lists each document's defs.
	byFile [][]int
	// refs counts each def's references from other documents.
	refs []int
	// out[a] are the edges from document a to the documents whose symbols it references.
	out [][]Edge
}

// newGraph links document A to document B when A references a symbol defined in B. Each symbol
// adds the square root of its reference count to the edge's weight, so one symbol used everywhere
// does not outweigh many used a little. A document's references to itself, local symbols, and
// namespaces and parameters do not count.
func newGraph(idx *model.Index) *graph {
	n := len(idx.Documents)
	g := &graph{byFile: make([][]int, n), out: make([][]Edge, n)}
	fileOf := make(map[string]int, n)
	for i, d := range idx.Documents {
		if _, dup := fileOf[d.Path]; !dup {
			fileOf[d.Path] = i
		}
	}
	defOf := map[string]int{}
	for i := range idx.Symbols {
		s := &idx.Symbols[i]
		if s.Definition == nil || !mappable(s) {
			continue
		}
		f, ok := fileOf[s.Definition.Path]
		if _, dup := defOf[s.ID]; !ok || dup {
			continue
		}
		name := s.DisplayName
		if name == "" {
			name = nameOf(s.ID)
		}
		defOf[s.ID] = len(g.defs)
		g.byFile[f] = append(g.byFile[f], len(g.defs))
		g.defs = append(g.defs, def{sym: s, file: f, name: name})
	}
	g.refs = make([]int, len(g.defs))

	// Both maps are filled in occurrence order and read back in that order or sorted, so the
	// floating-point sums come out the same on every run.
	count := map[int]int{}
	weight := map[int]float64{}
	var touched, targets []int
	for a, d := range idx.Documents {
		clear(count)
		touched = touched[:0]
		for _, o := range d.Occurrences {
			if !o.Roles.Has(model.RoleReference) {
				continue
			}
			n, ok := defOf[o.Symbol]
			if !ok || g.defs[n].file == a {
				continue
			}
			if count[n] == 0 {
				touched = append(touched, n)
			}
			count[n]++
		}
		clear(weight)
		targets = targets[:0]
		for _, n := range touched {
			g.refs[n] += count[n]
			b := g.defs[n].file
			if _, ok := weight[b]; !ok {
				targets = append(targets, b)
			}
			weight[b] += math.Sqrt(float64(count[n]))
		}
		slices.Sort(targets)
		for _, b := range targets {
			g.out[a] = append(g.out[a], Edge{To: b, Weight: weight[b]})
		}
	}
	return g
}

// mappable reports whether the map shows s: not a local, a namespace or a parameter.
func mappable(s *model.Symbol) bool {
	if s.ID == "" || strings.HasPrefix(s.ID, "local ") {
		return false
	}
	// SCIP's descriptor suffixes: / a namespace, (x) a parameter, [T] a type parameter.
	switch s.ID[len(s.ID)-1] {
	case '/', ')', ']':
		return false
	}
	switch s.Kind {
	case "Package", "PackageObject", "Namespace", "Module", "Parameter", "SelfParameter", "TypeParameter":
		return false
	}
	return true
}

// isMember reports whether kind is a field or an enum member, which the line of the struct or enum
// that holds it already shows. The map leaves them out, but references to them still link files.
func isMember(kind string) bool {
	switch kind {
	case "Field", "StaticField", "StaticDataMember", "EnumMember", "Property", "StaticProperty":
		return true
	}
	return false
}

// nameOf is the name in a SCIP symbol's last descriptor: Add in `.../Adder#Add.`, Do in
// `.../T#Do(+1).`, and a backquoted name without its quotes.
func nameOf(id string) string {
	s := id
	if s == "" {
		return ""
	}
	switch s[len(s)-1] {
	case '.', '#', '/', ':', '!':
		s = s[:len(s)-1]
	}
	if strings.HasSuffix(s, ")") {
		if i := strings.LastIndexByte(s, '('); i >= 0 {
			s = s[:i]
		}
	}
	if strings.HasSuffix(s, "`") {
		if i := strings.LastIndexByte(s[:len(s)-1], '`'); i >= 0 {
			return s[i+1 : len(s)-1]
		}
	}
	if i := strings.LastIndexAny(s, " /#.`"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// personalise is the jump vector for words: each document gets one for every word its path or the
// name of a symbol it defines contains, ignoring case. It is nil, for a uniform jump, when no word
// matches anything; matched counts the documents that matched.
func (g *graph) personalise(idx *model.Index, words []string) (personal []float64, matched int) {
	if len(words) == 0 {
		return nil, 0
	}
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	names := make([]string, len(g.defs))
	for i, d := range g.defs {
		names[i] = strings.ToLower(d.name)
	}
	personal = make([]float64, len(idx.Documents))
	for f, d := range idx.Documents {
		path := strings.ToLower(d.Path)
		for _, w := range lower {
			hit := strings.Contains(path, w)
			for _, n := range g.byFile[f] {
				if hit {
					break
				}
				hit = strings.Contains(names[n], w)
			}
			if hit {
				personal[f]++
			}
		}
		if personal[f] > 0 {
			matched++
		}
	}
	if matched == 0 {
		return nil, 0
	}
	return personal, matched
}

// Rank ranks idx's files by PageRank over the reference graph, personalised towards the files
// that match words (see Words) when any do, and each file's symbols by their references from other
// files. Fields and enum members (see isMember) are not among a file's symbols and take no share of
// its rank, but references to them count as edges. Files that define no symbol the map shows are
// left out.
func Rank(idx *model.Index, words []string) Ranking {
	g := newGraph(idx)
	personal, matched := g.personalise(idx, words)
	ranks, _ := PageRank(g.out, personal)
	r := Ranking{Matched: matched}
	for f, all := range g.byFile {
		var defs []int
		for _, n := range all {
			if !isMember(g.defs[n].sym.Kind) {
				defs = append(defs, n)
			}
		}
		if len(defs) == 0 {
			continue
		}
		total := 0
		for _, n := range defs {
			total += g.refs[n] + 1
		}
		file := File{Path: idx.Documents[f].Path, Rank: ranks[f], Symbols: make([]Symbol, 0, len(defs))}
		for _, n := range defs {
			d := g.defs[n]
			file.Symbols = append(file.Symbols, Symbol{
				ID:        d.sym.ID,
				Name:      d.name,
				Kind:      d.sym.Kind,
				Signature: d.sym.Signature,
				Doc:       d.sym.Doc,
				Refs:      g.refs[n],
				Score:     ranks[f] * float64(g.refs[n]+1) / float64(total),
			})
		}
		slices.SortFunc(file.Symbols, func(a, b Symbol) int {
			return cmp.Or(cmp.Compare(b.Refs, a.Refs), strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
		})
		r.Files = append(r.Files, file)
	}
	slices.SortFunc(r.Files, func(a, b File) int {
		return cmp.Or(cmp.Compare(b.Rank, a.Rank), strings.Compare(a.Path, b.Path))
	})
	return r
}

// Words splits what --for gives into lower-case words, at white space and commas, each once.
func Words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) {
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}
