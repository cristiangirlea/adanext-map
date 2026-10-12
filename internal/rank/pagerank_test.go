package rank

import (
	"math"
	"testing"
)

// checkStationary fails unless rank solves the PageRank equations for out and the jump vector
// within tol, and sums to 1.
func checkStationary(t *testing.T, out [][]Edge, jump []float64, rank []float64, tol float64) {
	t.Helper()
	n := len(out)
	total, dangling, sum := make([]float64, n), 0.0, 0.0
	for i, es := range out {
		for _, e := range es {
			total[i] += e.Weight
		}
		if total[i] == 0 {
			dangling += rank[i]
		}
		sum += rank[i]
	}
	if math.Abs(sum-1) > tol {
		t.Errorf("ranks sum to %v, want 1", sum)
	}
	want := make([]float64, n)
	for i := range want {
		want[i] = Damping*dangling/float64(n) + (1-Damping)*jump[i]
	}
	for i, es := range out {
		for _, e := range es {
			want[e.To] += Damping * rank[i] * e.Weight / total[i]
		}
	}
	for i := range want {
		if math.Abs(want[i]-rank[i]) > tol {
			t.Errorf("node %d: rank %v, the equations give %v", i, rank[i], want[i])
		}
	}
}

func uniform(n int) []float64 {
	u := make([]float64, n)
	for i := range u {
		u[i] = 1 / float64(n)
	}
	return u
}

func TestPageRankPutsTheHubOfAStarFirst(t *testing.T) {
	// Four leaves point at node 0, which points nowhere.
	out := [][]Edge{nil, {{0, 1}}, {{0, 1}}, {{0, 1}}, {{0, 1}}}
	rank, iters := PageRank(out, nil)
	// Solved by hand: hub = 11/21, each leaf = 2.5/21.
	if math.Abs(rank[0]-11.0/21) > 1e-5 {
		t.Errorf("hub rank %v, want %v", rank[0], 11.0/21)
	}
	for i := 1; i < 5; i++ {
		if math.Abs(rank[i]-2.5/21) > 1e-5 {
			t.Errorf("leaf %d rank %v, want %v", i, rank[i], 2.5/21)
		}
		if rank[i] != rank[1] {
			t.Errorf("leaf %d rank %v differs from leaf 1's %v", i, rank[i], rank[1])
		}
	}
	if iters < 1 || iters >= maxIterations {
		t.Errorf("took %d iterations, want convergence within %d", iters, maxIterations)
	}
	checkStationary(t, out, uniform(5), rank, 1e-5)
}

func TestPageRankRisesAlongAChain(t *testing.T) {
	out := [][]Edge{{{1, 1}}, {{2, 1}}, {{3, 1}}, nil}
	rank, _ := PageRank(out, nil)
	for i := 1; i < len(rank); i++ {
		if rank[i] <= rank[i-1] {
			t.Errorf("rank %v: node %d is not above node %d", rank, i, i-1)
		}
	}
	checkStationary(t, out, uniform(4), rank, 1e-5)
}

func TestPageRankSpreadsDanglingNodesUniformly(t *testing.T) {
	rank, iters := PageRank([][]Edge{nil, nil, nil}, nil)
	for i, r := range rank {
		if math.Abs(r-1.0/3) > 1e-12 {
			t.Errorf("node %d rank %v, want 1/3", i, r)
		}
	}
	if iters != 1 {
		t.Errorf("a graph without edges took %d iterations, want 1", iters)
	}
}

func TestPageRankFollowsWeights(t *testing.T) {
	// Node 0 links to 1 with weight 3 and to 2 with weight 1.
	out := [][]Edge{{{1, 3}, {2, 1}}, nil, nil}
	rank, _ := PageRank(out, nil)
	if rank[1] <= rank[2] {
		t.Errorf("rank %v: the heavier edge's target is not above the lighter one's", rank)
	}
	checkStationary(t, out, uniform(3), rank, 1e-5)
}

func TestPageRankJumpsByThePersonalisation(t *testing.T) {
	out := [][]Edge{{{1, 1}}, nil, nil}
	plain, _ := PageRank(out, nil)
	personal := []float64{0, 0, 2}
	rank, _ := PageRank(out, personal)
	if rank[2] <= plain[2] || rank[2] <= rank[0] {
		t.Errorf("personalised to node 2: rank %v, unpersonalised %v", rank, plain)
	}
	checkStationary(t, out, []float64{0, 0, 1}, rank, 1e-5)
	// A personalisation that sums to zero is no personalisation.
	zero, _ := PageRank(out, []float64{0, 0, 0})
	for i := range zero {
		if zero[i] != plain[i] {
			t.Errorf("zero personalisation: rank %v, want %v", zero, plain)
		}
	}
}

func TestPageRankConvergesOrStopsAtTheLimit(t *testing.T) {
	// A long cycle with a heavy back edge converges slowly but surely.
	n := 200
	out := make([][]Edge, n)
	for i := range out {
		out[i] = []Edge{{(i + 1) % n, 1}, {0, 5}}
	}
	rank, iters := PageRank(out, nil)
	if iters > maxIterations {
		t.Fatalf("ran %d iterations, the limit is %d", iters, maxIterations)
	}
	if iters < maxIterations {
		checkStationary(t, out, uniform(n), rank, 1e-5)
	}
}

func TestPageRankOfNothingIsNothing(t *testing.T) {
	if rank, iters := PageRank(nil, nil); rank != nil || iters != 0 {
		t.Errorf("empty graph: rank %v after %d iterations", rank, iters)
	}
}
