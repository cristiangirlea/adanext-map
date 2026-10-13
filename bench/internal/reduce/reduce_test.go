package reduce_test

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
	"github.com/cristiangirlea/adanext-map/bench/internal/reduce"
)

func reduceOK(t *testing.T, m *manifest.Manifest, recs []record.Record) *reduce.Report {
	t.Helper()
	rep, err := reduce.Reduce(m, recs)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func cond(t *testing.T, rep *reduce.Report, id string) reduce.Condition {
	t.Helper()
	for _, c := range rep.Conditions {
		if c.Condition == id {
			return c
		}
	}
	t.Fatalf("no condition %q in %+v", id, rep.Conditions)
	return reduce.Condition{}
}

func total(r record.Record) int64 {
	var n int64
	for _, a := range r.Attempts {
		tok, _ := a.Usage.Tokens()
		n += tok.Total
	}
	return n
}

// fail makes a run fail its gates honestly: its last attempt exits 1.
func fail(r *record.Record) {
	r.Attempts[len(r.Attempts)-1].Exit = 1
	r.Attempts[len(r.Attempts)-1].Error = "exit status 1"
}

func scaleTokens(recs []record.Record, condition string, f float64) {
	for i := range recs {
		if recs[i].Condition != condition {
			continue
		}
		u := &recs[i].Attempts[0].Usage
		*u.Input = int64(float64(*u.Input) * f)
		*u.CacheRead = int64(float64(*u.CacheRead) * f)
	}
}

func TestReduceRefusesRecordsWithProblems(t *testing.T) {
	m := benchtest.Synthetic()
	recs := benchtest.Records(m)
	recs = append(recs, recs[0])
	if _, err := reduce.Reduce(m, recs); err == nil || !strings.Contains(err.Error(), "duplicate run id") {
		t.Fatalf("err %v", err)
	}
}

func TestCoverageAndSuccess(t *testing.T) {
	m := benchtest.Synthetic()
	rep := reduceOK(t, m, benchtest.Records(m))
	if len(rep.Cells) != 50 {
		t.Fatalf("%d cells", len(rep.Cells))
	}
	for _, c := range rep.Cells {
		if !c.Complete || c.Recorded != 3 || c.Planned != 3 || c.Successes != 3 {
			t.Fatalf("cell %+v", c)
		}
	}
	for _, c := range rep.Conditions {
		if c.Planned != 30 || c.Recorded != 30 || c.Successes != 30 || c.Measured != 30 || c.Unmeasured != 0 {
			t.Fatalf("condition %+v", c)
		}
	}
	// A missing record leaves its cell incomplete and is counted, not ignored.
	recs := benchtest.Records(m)
	recs = slices.DeleteFunc(recs, func(r record.Record) bool { return r.RunID == "t04.serena.fake.r2" })
	rep = reduceOK(t, m, recs)
	if c := cond(t, rep, "serena"); c.Recorded != 29 || c.Planned != 30 {
		t.Fatalf("serena %+v", c)
	}
}

func TestPercentilesAndMedian(t *testing.T) {
	xs := []float64{10, 1, 9, 2, 8, 3, 7, 4, 6, 5}
	if got := reduce.Percentile(xs, 50); got != 5 {
		t.Errorf("p50 %v", got)
	}
	if got := reduce.Percentile(xs, 95); got != 10 {
		t.Errorf("p95 %v", got)
	}
	if got := reduce.Median(xs); got != 5.5 {
		t.Errorf("median %v", got)
	}
	if !math.IsNaN(reduce.Median(nil)) || !math.IsNaN(reduce.Percentile(nil, 50)) {
		t.Error("an empty set has a median")
	}
}

func TestLatencyAndTokensPerCondition(t *testing.T) {
	m := benchtest.Synthetic()
	recs := benchtest.Records(m)
	rep := reduceOK(t, m, recs)
	var walls, totals []float64
	for _, r := range recs {
		if r.Condition == "none" {
			walls = append(walls, float64(r.Attempts[0].WallMS))
			totals = append(totals, float64(total(r)))
		}
	}
	c := cond(t, rep, "none")
	if c.P50MS != reduce.Percentile(walls, 50) || c.P95MS != reduce.Percentile(walls, 95) {
		t.Errorf("latency %v %v", c.P50MS, c.P95MS)
	}
	if c.MedianTokens != reduce.Median(totals) {
		t.Errorf("median tokens %v, want %v", c.MedianTokens, reduce.Median(totals))
	}
	if a := cond(t, rep, "adamap"); a.MedianTokens >= c.MedianTokens {
		t.Errorf("adamap %v, none %v", a.MedianTokens, c.MedianTokens)
	}
}

// Cost per success: every recorded run's tokens over the successes, failures included.
func TestCostPerSuccessCountsFailuresInTheNumerator(t *testing.T) {
	m := benchtest.Synthetic()
	recs := benchtest.Records(m)
	var sum int64
	for i := range recs {
		if recs[i].Condition == "adamap" {
			sum += total(recs[i])
			if recs[i].Task == "t00" {
				fail(&recs[i])
			}
		}
	}
	c := cond(t, reduceOK(t, m, recs), "adamap")
	if c.Successes != 27 || c.FailedAttempts != 3 {
		t.Fatalf("adamap %+v", c)
	}
	if want := float64(sum) / 27; math.Abs(c.TokensPerSuccess.Value-want) > 1e-6 || c.TokensPerSuccess.Undefined {
		t.Fatalf("tokens per success %+v, want %v", c.TokensPerSuccess, want)
	}

	for i := range recs {
		if recs[i].Condition == "adamap" {
			fail(&recs[i])
		}
	}
	c = cond(t, reduceOK(t, m, recs), "adamap")
	if !c.TokensPerSuccess.Undefined || c.Successes != 0 {
		t.Fatalf("zero successes gives %+v", c.TokensPerSuccess)
	}
}

// Missing usage is not zero: the run is counted as unmeasured, its cell is not complete, the
// cost per success is incomplete, and an estimate stays apart from reported tokens.
func TestMissingAndEstimatedUsageStayApart(t *testing.T) {
	m := benchtest.Synthetic()
	recs := benchtest.Records(m)
	benchtest.Find(recs, "t01.none.fake.r1").Attempts[0].Usage = record.Usage{Source: record.Unavailable}
	benchtest.Find(recs, "t01.none.fake.r2").Attempts[0].Usage = record.Usage{Source: record.Estimated, Estimator: "bytes/4", Total: benchtest.Int64(5)}
	rep := reduceOK(t, m, recs)
	c := cond(t, rep, "none")
	if c.Measured != 28 || c.Unmeasured != 2 || c.Estimated != 1 || c.MedianEstimated != 5 {
		t.Fatalf("none %+v", c)
	}
	if c.TokensPerSuccess.Incomplete != 2 {
		t.Fatalf("tokens per success %+v", c.TokensPerSuccess)
	}
	for _, cell := range rep.Cells {
		if cell.Task == "t01" && cell.Condition == "none" && cell.Complete {
			t.Fatal("a cell with unmeasured runs is complete")
		}
	}
	for _, p := range rep.Pairs {
		if p.Condition == "adamap" && p.Tasks != 9 {
			t.Fatalf("adamap pairs over %d tasks, want 9 (t01 is not comparable)", p.Tasks)
		}
	}
}

func TestPairedDifferencesOverComparableTasks(t *testing.T) {
	m := benchtest.Synthetic()
	rep := reduceOK(t, m, benchtest.Records(m))
	if len(rep.Pairs) != 4 {
		t.Fatalf("pairs %+v", rep.Pairs)
	}
	for _, p := range rep.Pairs {
		if p.Baseline != "none" || p.Tasks != 10 {
			t.Fatalf("pair %+v", p)
		}
		if p.Condition == "adamap" && (p.MedianRelative > -0.15 || p.MedianRelative < -0.25) {
			t.Fatalf("adamap relative difference %v, want about -0.2", p.MedianRelative)
		}
	}
}

// Warm: one build per cell, reused by the three repeats, so amortized is a third of the build.
// Cold: every run builds, reuse 1. The reuse is what was observed, never a hypothetical.
func TestPreparationOverheadFirstUseAndAmortized(t *testing.T) {
	for _, c := range []struct {
		cache     string
		builds    int
		reuse     int
		amortized float64
	}{{"warm", 10, 3, 10000}, {"cold", 30, 1, 30000}} {
		m := benchtest.Synthetic()
		m.Cache = c.cache
		rep := reduceOK(t, m, benchtest.Records(m))
		found := false
		for _, p := range rep.Prep {
			if p.Condition != "adamap" {
				continue
			}
			found = true
			if p.Builds != c.builds || p.MedianBuildMS != 30000 || !slices.Equal(p.ReuseCounts, []int{c.reuse}) || p.MedianAmortizedMS != c.amortized {
				t.Fatalf("%s: %+v", c.cache, p)
			}
		}
		if !found {
			t.Fatalf("%s: no adamap preparation in %+v", c.cache, rep.Prep)
		}
		for _, p := range rep.Prep {
			if p.Condition == "none" || p.Condition == "lsp" {
				t.Fatalf("%s has a preparation", p.Condition)
			}
		}
	}
}

func TestAdoptionRule(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(m *manifest.Manifest, recs []record.Record) []record.Record
		outcome string
		reason  string
	}{
		{"fewer tokens, equal success, latency within the bound", nil, "pass", ""},
		{"only 5% fewer tokens", func(m *manifest.Manifest, r []record.Record) []record.Record {
			scaleTokens(r, "adamap", 0.95/0.8)
			return r
		}, "fail", "at least 10%"},
		{"a new correctness failure", func(m *manifest.Manifest, r []record.Record) []record.Record {
			x := benchtest.Find(r, "t02.adamap.fake.r3")
			x.Checks[0].Exit = benchtest.Int(1)
			x.Checks[0].Passed = false
			return r
		}, "fail", "new correctness failure"},
		{"a new isolation failure", func(m *manifest.Manifest, r []record.Record) []record.Record {
			benchtest.Find(r, "t05.adamap.fake.r1").Isolation.Violations = []string{"read the fix revision"}
			return r
		}, "fail", "new isolation failure"},
		{"lower success", func(m *manifest.Manifest, r []record.Record) []record.Record {
			fail(benchtest.Find(r, "t06.adamap.fake.r2"))
			return r
		}, "fail", "success"},
		{"too slow", func(m *manifest.Manifest, r []record.Record) []record.Record {
			for i := range r {
				if r[i].Condition == "adamap" {
					r[i].Attempts[0].WallMS *= 2
				}
			}
			return r
		}, "fail", "latency"},
		{"a missing run", func(m *manifest.Manifest, r []record.Record) []record.Record {
			return slices.DeleteFunc(r, func(x record.Record) bool { return x.RunID == "t03.adamap.fake.r1" })
		}, "inconclusive", "missing coverage"},
		{"unavailable usage", func(m *manifest.Manifest, r []record.Record) []record.Record {
			benchtest.Find(r, "t03.none.fake.r1").Attempts[0].Usage = record.Usage{Source: record.Unavailable}
			return r
		}, "inconclusive", "missing coverage"},
		{"too few comparable tasks", func(m *manifest.Manifest, r []record.Record) []record.Record {
			m.Adoption.MinTasks = 11
			return r
		}, "inconclusive", "11"},
		// 0/0 is NaN, and a NaN compares false both ways: no data must never read as a pass.
		{"no tokens to compare", func(m *manifest.Manifest, r []record.Record) []record.Record {
			for i := range r {
				u := &r[i].Attempts[0].Usage
				u.Input, u.CacheCreation, u.CacheRead, u.Output = benchtest.Int64(0), benchtest.Int64(0), benchtest.Int64(0), benchtest.Int64(0)
			}
			return r
		}, "inconclusive", "no relative comparison"},
		{"no time to compare", func(m *manifest.Manifest, r []record.Record) []record.Record {
			for i := range r {
				r[i].Attempts[0].WallMS = 0
				if r[i].Index != nil && r[i].Index.BuildMS != nil {
					r[i].Index.BuildMS = benchtest.Int64(0)
				}
			}
			return r
		}, "inconclusive", "no relative comparison"},
		{"a smoke", func(m *manifest.Manifest, r []record.Record) []record.Record {
			m.Scale = "smoke"
			return r
		}, "inconclusive", "smoke"},
	}
	for _, c := range cases {
		m := benchtest.Synthetic()
		recs := benchtest.Records(m)
		if c.mutate != nil {
			recs = c.mutate(m, recs)
		}
		rep := reduceOK(t, m, recs)
		if len(rep.Adoption) != 1 {
			t.Fatalf("%s: verdicts %+v", c.name, rep.Adoption)
		}
		v := rep.Adoption[0]
		if v.Outcome != c.outcome || !strings.Contains(strings.Join(v.Reasons, "\n"), c.reason) {
			t.Errorf("%s: %s %q, want %s with %q", c.name, v.Outcome, v.Reasons, c.outcome, c.reason)
		}
	}
}

// A synthetic fixture is labelled synthetic in the title, at the top, and in every section, so no
// part of it can be quoted as a measurement.
func TestMarkdownLabelsSyntheticEverywhere(t *testing.T) {
	m := benchtest.Synthetic()
	md := reduceOK(t, m, benchtest.Records(m)).Markdown()
	lines := strings.Split(md, "\n")
	if !strings.HasPrefix(lines[0], "# ") || !strings.Contains(lines[0], "SYNTHETIC") {
		t.Fatalf("title %q", lines[0])
	}
	if !strings.Contains(md, "not measurements") {
		t.Fatal("no statement that these are not measurements")
	}
	sections := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			sections++
			if !strings.Contains(l, "synthetic") {
				t.Errorf("section %q is not labelled synthetic", l)
			}
		}
	}
	if sections < 8 {
		t.Fatalf("%d sections:\n%s", sections, md)
	}
	for _, want := range []string{"Adoption rule", "pass", "Preparation", "p95", "Paired", "Coverage", "Reproduce",
		"adamap-bench", "fake-model-1"} {
		if !strings.Contains(md, want) {
			t.Errorf("no %q in:\n%s", want, md)
		}
	}
}

func TestMarkdownSaysUndefinedIncompleteAndSmoke(t *testing.T) {
	m := benchtest.Synthetic()
	m.Scale = "smoke"
	recs := benchtest.Records(m)
	for i := range recs {
		if recs[i].Condition == "adamap" {
			fail(&recs[i])
		}
	}
	benchtest.Find(recs, "t01.none.fake.r1").Attempts[0].Usage = record.Usage{Source: record.Unavailable}
	md := reduceOK(t, m, recs).Markdown()
	for _, want := range []string{"SMOKE", "undefined (no successes)", "incomplete (1 run without reported usage)", "inconclusive"} {
		if !strings.Contains(md, want) {
			t.Errorf("no %q in:\n%s", want, md)
		}
	}
}
