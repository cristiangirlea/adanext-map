package rank

import "math"

const (
	// Damping is the chance that the random walk behind PageRank follows an edge rather than jumps.
	Damping = 0.85
	// tolerance is the total (L1) change in the ranks below which PageRank has converged.
	tolerance = 1e-6
	// maxIterations bounds PageRank when it converges slowly.
	maxIterations = 100
)

// Edge is a weighted link to node To.
type Edge struct {
	To     int
	Weight float64
}

// PageRank ranks the nodes of a weighted directed graph, out[i] being node i's edges; an edge
// without a positive weight is ignored. personal is where the walk jumps to, in proportion; nil, or
// one that is not positive somewhere, jumps uniformly. A node without edges spreads its rank over
// all nodes alike. It iterates until the ranks change by less than 1e-6 in total, or 100 times, and
// returns the ranks, which sum to 1, and the iterations it ran.
func PageRank(out [][]Edge, personal []float64) ([]float64, int) {
	n := len(out)
	if n == 0 {
		return nil, 0
	}
	jump := make([]float64, n)
	sum := 0.0
	if len(personal) == n {
		for _, p := range personal {
			if p > 0 {
				sum += p
			}
		}
	}
	for i := range jump {
		switch {
		case sum == 0:
			jump[i] = 1 / float64(n)
		case personal[i] > 0:
			jump[i] = personal[i] / sum
		}
	}
	total := make([]float64, n)
	for i, es := range out {
		for _, e := range es {
			if e.Weight > 0 {
				total[i] += e.Weight
			}
		}
	}

	rank, next := make([]float64, n), make([]float64, n)
	for i := range rank {
		rank[i] = 1 / float64(n)
	}
	for iter := 1; iter <= maxIterations; iter++ {
		clear(next)
		dangling := 0.0
		for i, es := range out {
			if total[i] == 0 {
				dangling += rank[i]
				continue
			}
			share := Damping * rank[i] / total[i]
			for _, e := range es {
				if e.Weight > 0 {
					next[e.To] += share * e.Weight
				}
			}
		}
		spread := Damping * dangling / float64(n)
		change := 0.0
		for i := range next {
			next[i] += spread + (1-Damping)*jump[i]
			change += math.Abs(next[i] - rank[i])
		}
		rank, next = next, rank
		if change < tolerance {
			return rank, iter
		}
	}
	return rank, maxIterations
}
