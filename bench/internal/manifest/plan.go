package manifest

import (
	"fmt"
	"math/rand/v2"
)

// Run is one planned run: a cell (task, condition, backend) and its repeat, at its place in the order.
type Run struct {
	Seq       int    `json:"seq"`
	ID        string `json:"id"`
	Task      string `json:"task"`
	Condition string `json:"condition"`
	Backend   string `json:"backend"`
	Repeat    int    `json:"repeat"`
	// Block is the group of runs that share task, repeat and backend, one per condition; Position
	// is the condition's place in it, counterbalanced across blocks.
	Block    int    `json:"block"`
	Position int    `json:"position"`
	Cache    string `json:"cache"`
	// Index is "build", "reuse", or empty for a condition without an index.
	Index string `json:"index,omitempty"`
}

// RunID is the run's identity, derived from its cell and repeat so that a record cannot name a
// run it is not. IDs hold no dot, so the parts cannot run together.
func RunID(task, condition, backend string, repeat int) string {
	return fmt.Sprintf("%s.%s.%s.r%d", task, condition, backend, repeat)
}

// Plan is every run in the order it executes. It is meaningful only for a manifest without
// structural issues.
//
// Blocks run repeat by repeat, then backend by backend, then task by task, so a warm index is
// built before any repeat reuses it. The conditions' base order is shuffled by the seed, and
// block k rotates it by k: a Latin square, so across any len(conditions) consecutive blocks each
// condition takes each position once.
func (m *Manifest) Plan() []Run {
	base := make([]Condition, len(m.Conditions))
	copy(base, m.Conditions)
	// Fisher-Yates over PCG's raw output, whose sequence Go keeps stable for a seed, so the
	// order reproduces on any Go version (rand.Shuffle's algorithm is not promised).
	pcg := rand.NewPCG(m.Order.Seed, 0x6164616d6170)
	for i := len(base) - 1; i > 0; i-- {
		j := int(pcg.Uint64() % uint64(i+1))
		base[i], base[j] = base[j], base[i]
	}

	var plan []Run
	block := 0
	for r := 1; r <= m.Repeats; r++ {
		for _, b := range m.Backends {
			for _, t := range m.Tasks {
				for pos := range base {
					c := base[(pos+block)%len(base)]
					index := ""
					if c.Index != nil {
						index = "build"
						if m.Cache == "warm" && r > 1 {
							index = "reuse"
						}
					}
					plan = append(plan, Run{
						Seq: len(plan) + 1, ID: RunID(t.ID, c.ID, b.ID, r),
						Task: t.ID, Condition: c.ID, Backend: b.ID, Repeat: r,
						Block: block, Position: pos, Cache: m.Cache, Index: index,
					})
				}
				block++
			}
		}
	}
	return plan
}
