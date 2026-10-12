package query

import (
	"cmp"
	"fmt"
	"strings"
)

// MaxCandidates is how many candidates an AmbiguousError lists.
const MaxCandidates = 20

// FindResult is the answer to Find: the matches up to the limit, and how many there were in all.
type FindResult struct {
	Query   string   `json:"query"`
	Total   int      `json:"total"`
	Symbols []Symbol `json:"symbols"`
}

// Short is the symbol on one line: `path:line kind name`, with `-` for what it does not have.
func (s Symbol) Short() string {
	loc := "-"
	if s.Path != "" {
		loc = fmt.Sprintf("%s:%d", s.Path, s.Line)
	}
	return loc + " " + cmp.Or(s.Kind, "-") + " " + s.Name
}

// AmbiguousError is Resolve's refusal of a name that fits more than one symbol.
type AmbiguousError struct {
	Name  string
	Total int
	// Candidates are the first MaxCandidates of them, in the order Find ranks them.
	Candidates []Symbol
}

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the name %q fits %d symbols: pass the symbol string of the one you mean", e.Name, e.Total)
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  %s  %s", c.Short(), c.ID)
	}
	if e.Total > len(e.Candidates) {
		fmt.Fprintf(&b, "\n  ... and %d more", e.Total-len(e.Candidates))
	}
	return b.String()
}

// tiers sort what a name matches: the display name equals it, equals it ignoring case, starts
// with it ignoring case, or contains it ignoring case. A symbol is in the first tier it fits.
const (
	tierExact = iota
	tierFold
	tierPrefix
	tierSubstring
	tierCount
)

// match puts each global symbol whose name fits name into its tier, among the first tiers. Within
// a tier the symbols are in ID order, which is the store's.
func (x *Index) match(name string, tiers int) [tierCount][]*entry {
	var out [tierCount][]*entry
	if name == "" {
		return out
	}
	lower := strings.ToLower(name)
	for _, e := range x.entries {
		tier := -1
		switch {
		case e.name == "":
		case e.name == name:
			tier = tierExact
		case e.lower == lower:
			tier = tierFold
		case strings.HasPrefix(e.lower, lower):
			tier = tierPrefix
		case strings.Contains(e.lower, lower):
			tier = tierSubstring
		}
		if tier >= 0 && tier < tiers {
			out[tier] = append(out[tier], e)
		}
	}
	return out
}

// Find lists the global symbols whose display name fits name: exact first, then equal ignoring
// case, then a prefix, then a substring, each group in the store's order. A symbol the indexer
// never described is matched by the name read out of its symbol string. limit caps the list, not
// Total; a limit of 0 or less means no cap.
func (x *Index) Find(name string, limit int) FindResult {
	res := FindResult{Query: name, Symbols: []Symbol{}}
	for _, tier := range x.match(name, tierCount) {
		res.Total += len(tier)
		for _, e := range tier {
			if limit <= 0 || len(res.Symbols) < limit {
				res.Symbols = append(res.Symbols, x.view(e))
			}
		}
	}
	return res
}

// Resolve finds the one symbol that spec means. spec is a SCIP symbol string, or a display name:
// a name must equal exactly one symbol's name, or failing that, exactly one ignoring case. A
// prefix or a fragment is not enough; that is what Find is for. A name that fits several symbols
// is an *AmbiguousError. Nothing fitting is ErrNoSymbol.
func (x *Index) Resolve(spec string) (Symbol, error) {
	if e := x.byID[spec]; e != nil {
		return x.view(e), nil
	}
	for _, tier := range x.match(spec, tierFold+1) {
		switch len(tier) {
		case 0:
		case 1:
			return x.view(tier[0]), nil
		default:
			amb := &AmbiguousError{Name: spec, Total: len(tier)}
			for _, e := range tier[:min(len(tier), MaxCandidates)] {
				amb.Candidates = append(amb.Candidates, x.view(e))
			}
			return Symbol{}, amb
		}
	}
	return Symbol{}, fmt.Errorf("%w: %q", ErrNoSymbol, spec)
}
