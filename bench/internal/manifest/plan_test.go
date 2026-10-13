package manifest_test

import (
	"slices"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

func TestPlanHoldsEveryCellOnceUnderItsRunID(t *testing.T) {
	m := benchtest.Pilot()
	plan := m.Plan()
	if len(plan) != m.PlannedRuns() {
		t.Fatalf("%d runs, want %d", len(plan), m.PlannedRuns())
	}
	seen := map[string]bool{}
	for i, r := range plan {
		if r.Seq != i+1 {
			t.Fatalf("run %d has seq %d", i, r.Seq)
		}
		if want := manifest.RunID(r.Task, r.Condition, r.Backend, r.Repeat); r.ID != want {
			t.Fatalf("run %d id %q, want %q", i, r.ID, want)
		}
		if seen[r.ID] {
			t.Fatalf("run %q twice", r.ID)
		}
		seen[r.ID] = true
	}
	if got := manifest.RunID("t01", "adamap", "claude", 2); got != "t01.adamap.claude.r2" {
		t.Fatalf("RunID = %q", got)
	}
}

// Within each block (one task, one repeat, one backend) every condition runs once, and across any
// five consecutive blocks every condition takes every position once: no condition always goes first.
func TestPlanCounterbalancesConditionOrder(t *testing.T) {
	m := benchtest.Pilot()
	plan := m.Plan()
	n := len(m.Conditions)
	blocks := map[int][]manifest.Run{}
	for _, r := range plan {
		blocks[r.Block] = append(blocks[r.Block], r)
	}
	for b, runs := range blocks {
		conds := map[string]bool{}
		for _, r := range runs {
			conds[r.Condition] = true
			if r.Task != runs[0].Task || r.Repeat != runs[0].Repeat || r.Backend != runs[0].Backend {
				t.Fatalf("block %d mixes cells: %+v", b, runs)
			}
		}
		if len(conds) != n || len(runs) != n {
			t.Fatalf("block %d: %d runs over %d conditions", b, len(runs), len(conds))
		}
	}
	for start := 0; start+n <= len(blocks); start += n {
		at := map[string]map[int]bool{}
		for b := start; b < start+n; b++ {
			for _, r := range blocks[b] {
				if at[r.Condition] == nil {
					at[r.Condition] = map[int]bool{}
				}
				at[r.Condition][r.Position] = true
			}
		}
		for c, pos := range at {
			if len(pos) != n {
				t.Fatalf("blocks %d-%d: %s takes positions %v", start, start+n-1, c, pos)
			}
		}
	}
}

func TestPlanIsFixedBySeed(t *testing.T) {
	a, b := benchtest.Pilot(), benchtest.Pilot()
	if !slices.Equal(a.Plan(), b.Plan()) {
		t.Fatal("the same manifest planned twice differs")
	}
	b.Order.Seed++
	if slices.Equal(a.Plan(), b.Plan()) {
		t.Fatal("another seed gives the same order")
	}
}

// Warm: the first repeat of a cell builds the index and the later ones reuse it; cold: every run
// builds. A condition without an index has nothing to build.
func TestPlanRegistersTheCacheTreatment(t *testing.T) {
	for _, cache := range []string{"warm", "cold"} {
		m := benchtest.Pilot()
		m.Cache = cache
		for _, r := range m.Plan() {
			want := ""
			if r.Condition == "adamap" || r.Condition == "codegraph" {
				want = "build"
				if cache == "warm" && r.Repeat > 1 {
					want = "reuse"
				}
			}
			if r.Index != want || r.Cache != cache {
				t.Fatalf("%s: run %s index %q cache %q, want %q", cache, r.ID, r.Index, r.Cache, want)
			}
		}
	}
	// Repeats run in order, so a reuse never comes before its build.
	built := map[string]bool{}
	for _, r := range benchtest.Pilot().Plan() {
		cell := r.Task + "." + r.Condition + "." + r.Backend
		if r.Index == "build" {
			built[cell] = true
		}
		if r.Index == "reuse" && !built[cell] {
			t.Fatalf("%s reuses an index not yet built", r.ID)
		}
	}
}
