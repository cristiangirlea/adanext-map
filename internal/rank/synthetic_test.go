package rank

import (
	"fmt"
	"math/rand/v2"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// synthetic is a made-up index, the same every time: files documents in 50 packages, each defining
// defs functions (some with a signature, some with a doc line) and holding occs occurrences in all.
// References lean towards the low-numbered files, as real code leans on a few core ones.
func synthetic(files, defs, occs int) *model.Index {
	r := rand.New(rand.NewPCG(1, 2))
	idx := &model.Index{Documents: make([]model.Document, files)}
	name := func(f, d int) string { return fmt.Sprintf("Sym%d_%d", f, d) }
	path := func(f int) string { return fmt.Sprintf("pkg%d/file%d.go", f%50, f) }
	for f := range files {
		doc := &idx.Documents[f]
		doc.Path = path(f)
		for d := range defs {
			rg := model.Range{StartLine: d, EndLine: d, EndCol: 8}
			s := model.Symbol{
				ID: id(name(f, d)), DisplayName: name(f, d), Kind: "Function",
				Definition: &model.Location{Path: doc.Path, Range: rg},
			}
			switch d % 3 {
			case 0:
				s.Signature = fmt.Sprintf("func %s(ctx context.Context, n int) (string, error)", s.DisplayName)
			case 1:
				s.Doc = fmt.Sprintf("%s does the %dth thing.", s.DisplayName, d)
			}
			idx.Symbols = append(idx.Symbols, s)
			doc.Occurrences = append(doc.Occurrences, model.Occurrence{Range: rg, Symbol: s.ID, Roles: model.RoleDefinition})
		}
		for o := defs; o < occs; o++ {
			target := r.IntN(r.IntN(files) + 1)
			doc.Occurrences = append(doc.Occurrences, model.Occurrence{
				Range:  model.Range{StartLine: o, EndLine: o, EndCol: 8},
				Symbol: id(name(target, r.IntN(defs))),
				Roles:  model.RoleReference,
			})
		}
	}
	return idx
}
