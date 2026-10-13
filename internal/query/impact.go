package query

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// DefaultDepth is how far Impact and ImpactFile look when not told.
const DefaultDepth = 3

// Impacted is a symbol that depends on the target, directly or through other callers. Depth is
// the number of calls between it and the target (1 for a direct caller), and Via the symbol it
// calls on the way: the target itself at depth 1, otherwise the one at depth-1 that found it.
type Impacted struct {
	Symbol Symbol `json:"symbol"`
	Depth  int    `json:"depth"`
	Via    string `json:"via"`
}

// ImpactResult is what depends on a symbol or a file. Kind is "symbol" or "file", Target the symbol
// string or the path, and Depth the limit that was applied. Impacted is by depth, then by place.
// Unattributed counts the references the walk met, to the target or to a symbol whose callers it
// looked up, that lie in no definition (as in CallersResult), so it could not follow them.
type ImpactResult struct {
	Target       string     `json:"target"`
	Kind         string     `json:"kind"`
	Depth        int        `json:"depth"`
	Impacted     []Impacted `json:"impacted"`
	Unattributed int        `json:"unattributed,omitempty"`
}

// Impact lists what depends on the symbol id by callers, transitively and breadth-first, down to
// depth calls (DefaultDepth if depth is less than 1). A symbol is listed once, at the shallowest
// depth it is reached, and the target never lists itself, so a cycle ends the walk instead of
// repeating. An id the index does not know is ErrNoSymbol.
func (x *Index) Impact(id string, depth int) (ImpactResult, error) {
	if x.byID[id] == nil {
		return ImpactResult{}, fmt.Errorf("%w: %q", ErrNoSymbol, id)
	}
	return x.impact(id, "symbol", []string{id}, depth), nil
}

// ImpactFile is Impact for a file: what depends on any global symbol the document at path
// defines. The file's own symbols are the seeds of the walk and are not listed, even if they call
// each other. A path the index has no document for is ErrNoDocument.
func (x *Index) ImpactFile(path string, depth int) (ImpactResult, error) {
	di, ok := x.docs[path]
	if !ok {
		return ImpactResult{}, ErrNoDocument
	}
	var seeds []string
	seen := map[string]bool{}
	for _, r := range x.defsIn(di) {
		if id := x.src.Documents[di].Occurrences[r].Symbol; !seen[id] {
			seen[id] = true
			seeds = append(seeds, id)
		}
	}
	return x.impact(path, "file", seeds, depth), nil
}

// defsIn lists the occurrences in document di that define a global symbol, by position.
func (x *Index) defsIn(di int) []int32 {
	d := &x.src.Documents[di]
	var out []int32
	for oi := range d.Occurrences {
		if o := &d.Occurrences[oi]; o.Roles.Has(model.RoleDefinition) && o.Symbol != "" && !isLocal(o.Symbol) {
			out = append(out, int32(oi))
		}
	}
	slices.SortStableFunc(out, func(a, b int32) int { return compareRange(d.Occurrences[a].Range, d.Occurrences[b].Range) })
	return out
}

// impact walks the callers of seeds outward, a level at a time.
func (x *Index) impact(target, kind string, seeds []string, depth int) ImpactResult {
	if depth < 1 {
		depth = DefaultDepth
	}
	res := ImpactResult{Target: target, Kind: kind, Depth: depth, Impacted: []Impacted{}}
	visited := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		visited[s] = true
	}
	frontier := seeds
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var level []Impacted
		for _, id := range frontier {
			res.Unattributed += x.holders(id, func(r occRef, sc *scope) {
				caller := x.src.Documents[r.doc].Occurrences[sc.occ].Symbol
				if !visited[caller] {
					visited[caller] = true
					level = append(level, Impacted{Symbol: x.viewAt(x.byID[caller], occRef{r.doc, sc.occ}), Depth: d, Via: id})
				}
			})
		}
		slices.SortFunc(level, func(a, b Impacted) int {
			return cmp.Or(strings.Compare(a.Symbol.Path, b.Symbol.Path), cmp.Compare(a.Symbol.Line, b.Symbol.Line),
				strings.Compare(a.Symbol.ID, b.Symbol.ID))
		})
		frontier = frontier[:0:0]
		for _, i := range level {
			frontier = append(frontier, i.Symbol.ID)
		}
		res.Impacted = append(res.Impacted, level...)
	}
	return res
}
