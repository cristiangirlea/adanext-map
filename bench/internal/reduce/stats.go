package reduce

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

// run is what the reducer reads from one checked record.
type run struct {
	rec       *record.Record
	supported bool
	success   bool
	wallMS    float64
	tokens    float64 // reported: every attempt and overhead complete
	reported  bool
	estimate  float64 // reported plus estimated parts, when no part is unavailable and one is estimated
	estimated bool
	spend     float64
	hasSpend  bool
	apiEq     float64
	hasAPIEq  bool
	passed    map[string]bool // executable check id -> passed
}

func readRun(m *manifest.Manifest, r *record.Record) run {
	x := run{rec: r, supported: r.Unsupported == "", success: record.Success(m, r), passed: map[string]bool{}}
	reported, estimated, unavailable := 0, 0, 0
	usage := func(u record.Usage) {
		if t, ok := u.Tokens(); ok {
			reported++
			x.tokens += float64(t.Total)
			x.estimate += float64(t.Total)
			return
		}
		if u.Source == record.Estimated && u.Total != nil {
			estimated++
			x.estimate += float64(*u.Total)
			return
		}
		unavailable++
	}
	x.hasSpend, x.hasAPIEq = len(r.Attempts) > 0, len(r.Attempts) > 0
	for _, a := range r.Attempts {
		x.wallMS += float64(a.WallMS)
		usage(a.Usage)
		if a.Cost.Source != record.Unavailable && a.Cost.Kind == "spend" && a.Cost.USD != nil {
			x.spend += *a.Cost.USD
		} else {
			x.hasSpend = false
		}
		if a.Cost.Source != record.Unavailable && a.Cost.Kind == "api-equivalent" && a.Cost.USD != nil {
			x.apiEq += *a.Cost.USD
		} else {
			x.hasAPIEq = false
		}
	}
	for _, o := range r.Overhead {
		usage(o.Usage)
	}
	x.reported = x.supported && unavailable == 0 && estimated == 0 && reported > 0
	x.estimated = x.supported && unavailable == 0 && estimated > 0
	for _, c := range r.Checks {
		if c.Type != manifest.CheckReview {
			x.passed[c.ID] = c.Passed
		}
	}
	return x
}

// Reduce checks recs against m and reduces them. A manifest with structural issues, or a record
// set with problems, is refused: nothing is reduced from records Check would not accept.
func Reduce(m *manifest.Manifest, recs []record.Record) (*Report, error) {
	var structural []string
	for _, i := range m.Validate() {
		if !i.Blocker {
			structural = append(structural, i.String())
		}
	}
	if len(structural) > 0 {
		return nil, fmt.Errorf("the manifest has %d structural issues: %s", len(structural), strings.Join(first(structural, 5), "; "))
	}
	if problems := record.Check(m, recs); len(problems) > 0 {
		var ps []string
		for _, p := range problems {
			ps = append(ps, p.String())
		}
		return nil, fmt.Errorf("%d problems in the records: %s", len(ps), strings.Join(first(ps, 10), "; "))
	}
	rep := &Report{Manifest: m, Synthetic: m.Synthetic, Smoke: m.Scale == "smoke", Records: len(recs)}
	runs := map[string]run{}
	for i := range recs {
		runs[recs[i].RunID] = readRun(m, &recs[i])
	}
	// cellRuns: a cell's recorded runs in plan order.
	cellRuns := map[string][]run{}
	for _, p := range m.Plan() {
		if r, ok := runs[p.ID]; ok {
			k := cellKey(p.Task, p.Condition, p.Backend)
			cellRuns[k] = append(cellRuns[k], r)
		}
	}
	share := amortize(m, cellRuns, rep)
	for _, b := range m.Backends {
		for _, c := range m.Conditions {
			rep.Conditions = append(rep.Conditions, conditionStats(m, c, b, cellRuns))
			for _, t := range m.Tasks {
				rep.Cells = append(rep.Cells, cellStats(m, t, c, b, cellRuns[cellKey(t.ID, c.ID, b.ID)]))
			}
		}
		rep.Pairs = append(rep.Pairs, pairs(m, b, rep.Cells, cellRuns)...)
		rep.Adoption = append(rep.Adoption, adopt(m, b, rep, cellRuns, share))
	}
	return rep, nil
}

func cellKey(task, condition, backend string) string { return task + "." + condition + "." + backend }

func first(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], fmt.Sprintf("and %d more", len(xs)-n))
	}
	return xs
}

func cellStats(m *manifest.Manifest, t manifest.Task, c manifest.Condition, b manifest.Backend, runs []run) Cell {
	cell := Cell{Task: t.ID, Condition: c.ID, Backend: b.ID, Planned: m.Repeats, Recorded: len(runs)}
	for _, r := range runs {
		if !r.supported {
			cell.Unsupported++
		}
		if r.reported {
			cell.Measured++
		}
		if r.success {
			cell.Successes++
		}
	}
	cell.Complete = cell.Recorded == cell.Planned && cell.Unsupported == 0 && cell.Measured == cell.Recorded
	return cell
}

func conditionStats(m *manifest.Manifest, c manifest.Condition, b manifest.Backend, cellRuns map[string][]run) Condition {
	s := Condition{Condition: c.ID, Backend: b.ID, Kind: c.Kind, Planned: len(m.Tasks) * m.Repeats}
	var walls, tokens, estimates, tools, reads, readBytes []float64
	var tokenSum, spendSum, apiSum float64
	spendMissing, apiMissing := 0, 0
	for _, t := range m.Tasks {
		for _, r := range cellRuns[cellKey(t.ID, c.ID, b.ID)] {
			s.Recorded++
			if !r.supported {
				s.Unsupported++
				continue
			}
			if r.success {
				s.Successes++
			}
			if len(r.rec.Isolation.Violations) > 0 {
				s.IsolationFailures++
			}
			for _, a := range r.rec.Attempts {
				s.Attempts++
				if !a.Completed() {
					s.FailedAttempts++
				}
			}
			walls = append(walls, r.wallMS)
			switch {
			case r.reported:
				s.Measured++
				tokens = append(tokens, r.tokens)
				tokenSum += r.tokens
			case r.estimated:
				s.Estimated++
				estimates = append(estimates, r.estimate)
			}
			if r.hasSpend {
				spendSum += r.spend
			} else {
				spendMissing++
			}
			if r.hasAPIEq {
				apiSum += r.apiEq
			} else {
				apiMissing++
			}
			if obs := observed(r.rec); obs.Observable {
				s.Observable++
				n := 0
				for _, v := range obs.ToolCalls {
					n += v
				}
				tools = append(tools, float64(n))
				if obs.SourceReads != nil {
					reads = append(reads, float64(*obs.SourceReads))
				}
				if obs.SourceBytes != nil {
					readBytes = append(readBytes, float64(*obs.SourceBytes))
				}
			}
		}
	}
	s.Unmeasured = s.Recorded - s.Unsupported - s.Measured
	s.P50MS, s.P95MS = Percentile(walls, 50), Percentile(walls, 95)
	s.MedianTokens, s.MedianEstimated = Median(tokens), Median(estimates)
	s.TokensPerSuccess = ratio(tokenSum, s.Successes, s.Unmeasured)
	s.SpendPerSuccess = ratio(spendSum, s.Successes, spendMissing)
	s.APIEquivalent = ratio(apiSum, s.Successes, apiMissing)
	s.MedianToolCalls, s.MedianReads, s.MedianReadBytes = Median(tools), Median(reads), Median(readBytes)
	return s
}

// observed sums what every attempt was seen to do, failed attempts included; a run is observable
// only when every attempt was.
func observed(r *record.Record) record.Observed {
	if len(r.Attempts) == 0 {
		return record.Observed{}
	}
	out := record.Observed{Observable: true, ToolCalls: map[string]int{}}
	var reads int
	var bytes int64
	readsKnown, bytesKnown := true, true
	for _, a := range r.Attempts {
		if !a.Observed.Observable {
			return record.Observed{}
		}
		for k, v := range a.Observed.ToolCalls {
			out.ToolCalls[k] += v
		}
		if a.Observed.SourceReads != nil {
			reads += *a.Observed.SourceReads
		} else {
			readsKnown = false
		}
		if a.Observed.SourceBytes != nil {
			bytes += *a.Observed.SourceBytes
		} else {
			bytesKnown = false
		}
	}
	if readsKnown {
		out.SourceReads = &reads
	}
	if bytesKnown {
		out.SourceBytes = &bytes
	}
	return out
}

func ratio(sum float64, successes, missing int) Ratio {
	if successes == 0 {
		return Ratio{Undefined: true, Incomplete: missing}
	}
	return Ratio{Value: sum / float64(successes), Incomplete: missing}
}

func pairs(m *manifest.Manifest, b manifest.Backend, cells []Cell, cellRuns map[string][]run) []Pair {
	complete := map[string]bool{}
	for _, c := range cells {
		if c.Backend == b.ID && c.Complete {
			complete[cellKey(c.Task, c.Condition, c.Backend)] = true
		}
	}
	base := m.Adoption.Baseline
	var out []Pair
	for _, c := range m.Conditions {
		if c.ID == base {
			continue
		}
		p := Pair{Condition: c.ID, Baseline: base, Backend: b.ID}
		var diffs, rels []float64
		for _, t := range m.Tasks {
			kc, kb := cellKey(t.ID, c.ID, b.ID), cellKey(t.ID, base, b.ID)
			if !complete[kc] || !complete[kb] {
				continue
			}
			p.Tasks++
			mc, mb := Median(tokensOf(cellRuns[kc])), Median(tokensOf(cellRuns[kb]))
			diffs = append(diffs, mc-mb)
			if mb > 0 {
				rels = append(rels, (mc-mb)/mb)
			}
			p.SuccessDiff += successesOf(cellRuns[kc]) - successesOf(cellRuns[kb])
		}
		p.MedianDiff, p.MedianRelative = Median(diffs), Median(rels)
		out = append(out, p)
	}
	return out
}

func tokensOf(runs []run) []float64 {
	var out []float64
	for _, r := range runs {
		if r.reported {
			out = append(out, r.tokens)
		}
	}
	return out
}

func successesOf(runs []run) int {
	n := 0
	for _, r := range runs {
		if r.success {
			n++
		}
	}
	return n
}

// amortize fills rep.Prep and returns each run's share of the build it used: the build's time
// over the runs observed to use it, the build's own run and the later reuses of its digest in the
// cell. The reuse count is what the records show, never a hypothetical.
func amortize(m *manifest.Manifest, cellRuns map[string][]run, rep *Report) map[string]float64 {
	share := map[string]float64{}
	for _, b := range m.Backends {
		for _, c := range m.Conditions {
			if c.Index == nil {
				continue
			}
			p := Prep{Condition: c.ID, Backend: b.ID}
			var builds, amort, refresh, sizes []float64
			reuse := map[int]bool{}
			for _, t := range m.Tasks {
				runs := cellRuns[cellKey(t.ID, c.ID, b.ID)]
				uses := map[int]int{} // index of a build in runs -> runs that used it
				for i, r := range runs {
					x := r.rec.Index
					if !r.supported || x == nil {
						continue
					}
					sizes = append(sizes, float64(x.Bytes))
					for _, f := range x.Refreshes {
						p.Refreshes++
						refresh = append(refresh, float64(f.MS))
					}
					if !x.Reused {
						uses[i]++
						continue
					}
					for j := i - 1; j >= 0; j-- { // the latest earlier build of the same digest
						if y := runs[j].rec.Index; y != nil && !y.Reused && y.Digest == x.Digest {
							uses[j]++
							break
						}
					}
				}
				for i, r := range runs {
					x := r.rec.Index
					if !r.supported || x == nil || x.Reused || x.BuildMS == nil {
						continue
					}
					p.Builds++
					ms := float64(*x.BuildMS)
					builds = append(builds, ms)
					reuse[uses[i]] = true
					a := ms / float64(uses[i])
					amort = append(amort, a)
					share[r.rec.RunID] = a
					for j := i + 1; j < len(runs); j++ {
						y := runs[j].rec.Index
						if y != nil && !y.Reused {
							break
						}
						if y != nil && y.Digest == x.Digest {
							share[runs[j].rec.RunID] = a
						}
					}
				}
			}
			for n := range reuse {
				p.ReuseCounts = append(p.ReuseCounts, n)
			}
			sort.Ints(p.ReuseCounts)
			p.MedianBuildMS, p.MedianAmortizedMS = Median(builds), Median(amort)
			p.MedianRefreshMS, p.MedianIndexBytes = Median(refresh), Median(sizes)
			rep.Prep = append(rep.Prep, p)
		}
	}
	return share
}

// adopt evaluates the pre-registered adamap-versus-no-tool rule of issue #6 for one backend.
func adopt(m *manifest.Manifest, b manifest.Backend, rep *Report, cellRuns map[string][]run, share map[string]float64) Verdict {
	a := m.Adoption
	v := Verdict{Backend: b.ID}
	if rep.Smoke {
		v.Outcome = "inconclusive"
		v.Reasons = []string{"a smoke is a wiring check: the rule is evaluated on the pilot only"}
		return v
	}
	// A new deterministic failure fails the rule whatever the coverage: a check that passed in
	// every baseline run of a task and failed in a candidate run, or an isolation failure the
	// baseline did not have on that task.
	var failures []string
	for _, t := range m.Tasks {
		cr := supportedOf(cellRuns[cellKey(t.ID, a.Candidate, b.ID)])
		br := supportedOf(cellRuns[cellKey(t.ID, a.Baseline, b.ID)])
		if len(cr) == 0 || len(br) == 0 {
			continue
		}
		for _, ch := range t.Checks {
			if ch.Type == manifest.CheckReview {
				continue
			}
			baseAll := true
			for _, r := range br {
				baseAll = baseAll && r.passed[ch.ID]
			}
			for _, r := range cr {
				if baseAll && !r.passed[ch.ID] {
					failures = append(failures, fmt.Sprintf("new correctness failure: task %s, check %s, run %s", t.ID, ch.ID, r.rec.RunID))
				}
			}
		}
		baseIsolated := true
		for _, r := range br {
			baseIsolated = baseIsolated && len(r.rec.Isolation.Violations) == 0
		}
		for _, r := range cr {
			if baseIsolated && len(r.rec.Isolation.Violations) > 0 {
				failures = append(failures, fmt.Sprintf("new isolation failure: run %s (%s)", r.rec.RunID, strings.Join(r.rec.Isolation.Violations, "; ")))
			}
		}
	}
	if len(failures) > 0 {
		v.Outcome = "fail"
		v.Reasons = failures
		return v
	}
	complete := map[string]bool{}
	for _, c := range rep.Cells {
		if c.Backend == b.ID && c.Complete {
			complete[cellKey(c.Task, c.Condition, c.Backend)] = true
		}
	}
	incomplete := 0
	var comparable []manifest.Task
	for _, t := range m.Tasks {
		ok := true
		for _, id := range []string{a.Candidate, a.Baseline} {
			if !complete[cellKey(t.ID, id, b.ID)] {
				incomplete++
				ok = false
			}
		}
		if ok {
			comparable = append(comparable, t)
		}
	}
	if incomplete > 0 {
		v.Outcome = "inconclusive"
		v.Reasons = []string{fmt.Sprintf("missing coverage: %d of %d cells of %s and %s are incomplete (missing or unsupported runs, or usage not reported); missing coverage prevents a measured-savings claim",
			incomplete, 2*len(m.Tasks), a.Candidate, a.Baseline)}
		return v
	}
	if len(comparable) < a.MinTasks {
		v.Outcome = "inconclusive"
		v.Reasons = []string{fmt.Sprintf("%d tasks with complete comparable cells, the rule needs %d", len(comparable), a.MinTasks)}
		return v
	}
	var cTok, bTok, cLat, bLat []float64
	cSucc, bSucc := 0, 0
	for _, t := range comparable {
		for _, r := range cellRuns[cellKey(t.ID, a.Candidate, b.ID)] {
			cTok = append(cTok, r.tokens)
			cLat = append(cLat, r.wallMS+share[r.rec.RunID])
			if r.success {
				cSucc++
			}
		}
		for _, r := range cellRuns[cellKey(t.ID, a.Baseline, b.ID)] {
			bTok = append(bTok, r.tokens)
			bLat = append(bLat, r.wallMS+share[r.rec.RunID])
			if r.success {
				bSucc++
			}
		}
	}
	mc, mb := Median(cTok), Median(bTok)
	lc, lb := Percentile(cLat, 50), Percentile(bLat, 50)
	// A relative drop needs a baseline above zero; 0/0 would be NaN, which compares false both
	// ways and would read as a pass.
	if !(mb > 0) || !(lb > 0) {
		v.Outcome = "inconclusive"
		v.Reasons = []string{fmt.Sprintf("the baseline's median tokens (%.0f) or p50 latency (%.1fs) is not above zero: no relative comparison", mb, lb/1000)}
		return v
	}
	pass := true
	if cSucc >= bSucc {
		v.Reasons = append(v.Reasons, fmt.Sprintf("success: %d of %d runs, the baseline %d: equal or better", cSucc, len(cTok), bSucc))
	} else {
		pass = false
		v.Reasons = append(v.Reasons, fmt.Sprintf("success: %d of %d runs is below the baseline's %d", cSucc, len(cTok), bSucc))
	}
	drop := 1 - mc/mb
	word := "meets"
	if !(drop >= a.MinTokenReduction) {
		pass, word = false, "misses"
	}
	v.Reasons = append(v.Reasons, fmt.Sprintf("tokens: median total model tokens %.0f against %.0f, %.1f%% lower, which %s the rule's at least %.0f%%",
		mc, mb, 100*drop, word, 100*a.MinTokenReduction))
	lr := lc / lb
	word = "within"
	if !(lr <= a.MaxLatencyRatio) {
		pass, word = false, "beyond"
	}
	v.Reasons = append(v.Reasons, fmt.Sprintf("latency: p50 %.1fs against %.1fs with preparation amortized at the observed reuse, ratio %.2f, %s the registered bound %.2f",
		lc/1000, lb/1000, lr, word, a.MaxLatencyRatio))
	v.Outcome = "pass"
	if !pass {
		v.Outcome = "fail"
	}
	return v
}

func supportedOf(runs []run) []run {
	var out []run
	for _, r := range runs {
		if r.supported {
			out = append(out, r)
		}
	}
	return out
}

// Percentile is the nearest-rank percentile of xs (p in (0, 100]); NaN for an empty set.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

// Median is the middle of xs, the mean of the two middle values for an even count; NaN for an
// empty set.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
