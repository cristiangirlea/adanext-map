package reduce

import (
	"fmt"
	"math"
	"strings"
)

// Markdown is the report as bench/RESULTS.md. A synthetic report says so in its title, at the
// top, in every section heading and at the end; a smoke says it is a smoke the same way.
func (r *Report) Markdown() string {
	m := r.Manifest
	var b strings.Builder
	label := ""
	switch {
	case r.Synthetic && r.Smoke:
		label = " (synthetic smoke)"
	case r.Synthetic:
		label = " (synthetic)"
	case r.Smoke:
		label = " (smoke)"
	}
	title := "# Benchmark results: " + m.Name
	if r.Synthetic {
		title += " - SYNTHETIC FIXTURE"
	}
	if r.Smoke {
		title += " - SMOKE"
	}
	fmt.Fprintf(&b, "%s\n\n", title)
	banner := func() {
		if r.Synthetic {
			b.WriteString("> **SYNTHETIC FIXTURE: these are not measurements.** The records were made by the fake\n" +
				"> backend or by test code; no model ran. Nothing here may be quoted as an adamap result.\n\n")
		}
		if r.Smoke {
			b.WriteString("> **SMOKE: a wiring check, not the pilot.** Its numbers are not a comparison.\n\n")
		}
	}
	banner()
	section := func(name string) { fmt.Fprintf(&b, "## %s%s\n\n", name, label) }

	section("Status")
	fmt.Fprintf(&b, "Manifest `%s`, scale %s, %d records of %d planned runs. Written by `adamap-bench reduce`.\n\n",
		m.Name, m.Scale, r.Records, m.PlannedRuns())

	section("Identities")
	b.WriteString("| condition | kind | mode | components |\n|---|---|---|---|\n")
	for _, c := range m.Conditions {
		var comps []string
		for _, x := range c.Components {
			s := x.Name + " " + x.Version
			if x.Source != "" {
				s += " (" + x.Source + ")"
			}
			comps = append(comps, s)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.ID, c.Kind, dash(c.Mode), dash(strings.Join(comps, ", ")))
	}
	b.WriteString("\n| backend | CLI | version | model | effort | plan |\n|---|---|---|---|---|---|\n")
	for _, x := range m.Backends {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", x.ID, x.CLI, x.Version, x.Model, dash(x.Effort), x.Plan)
	}
	e := m.Execution
	fmt.Fprintf(&b, "\nFixed in every cell: timeout %ds, at most %d attempts, permission mode %s, tools %s, context edit %s. "+
		"Cache treatment %s; order %s, seed %d; %d repeats.\n\n",
		e.TimeoutSeconds, e.MaxAttempts, e.PermissionMode, strings.Join(e.Tools, ", "), dash(e.ContextEdit), m.Cache, m.Order.Scheme, m.Order.Seed, m.Repeats)

	section("Coverage")
	b.WriteString("A cell is complete when every planned repeat is recorded, supported and has reported usage.\n\n")
	b.WriteString("| condition | backend | planned | recorded | unsupported | usage reported | complete cells |\n|---|---|---|---|---|---|---|\n")
	for _, c := range r.Conditions {
		done, all := 0, 0
		for _, cell := range r.Cells {
			if cell.Condition == c.Condition && cell.Backend == c.Backend {
				all++
				if cell.Complete {
					done++
				}
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %d of %d |\n", c.Condition, c.Backend, c.Planned, c.Recorded, c.Unsupported, c.Measured, done, all)
	}
	b.WriteString("\n")

	section("Cells")
	b.WriteString("Successes over recorded runs per task and condition; `*` marks an incomplete cell.\n\n")
	for _, be := range m.Backends {
		if len(m.Backends) > 1 {
			fmt.Fprintf(&b, "Backend %s:\n\n", be.ID)
		}
		b.WriteString("| task | kind |")
		for _, c := range m.Conditions {
			b.WriteString(" " + c.ID + " |")
		}
		b.WriteString("\n|---|---|" + strings.Repeat("---|", len(m.Conditions)) + "\n")
		for _, t := range m.Tasks {
			fmt.Fprintf(&b, "| %s | %s |", t.ID, t.Kind)
			for _, c := range m.Conditions {
				for _, cell := range r.Cells {
					if cell.Task == t.ID && cell.Condition == c.ID && cell.Backend == be.ID {
						mark := ""
						if !cell.Complete {
							mark = "*"
						}
						fmt.Fprintf(&b, " %d/%d%s |", cell.Successes, cell.Recorded, mark)
					}
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	section("Correctness and isolation")
	b.WriteString("Success is recomputed from the recorded check outputs: the last attempt completed, every executable check passed, the run stayed isolated. Reviews are not gates. Restarts after a limit count the times a run was started afresh because the plan's usage limit stopped it; they are no failed attempts.\n\n")
	b.WriteString("| condition | backend | successes | supported runs | attempts | failed attempts | isolation failures | restarts after a limit |\n|---|---|---|---|---|---|---|---|\n")
	for _, c := range r.Conditions {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %d | %d |\n", c.Condition, c.Backend, c.Successes, c.Recorded-c.Unsupported, c.Attempts, c.FailedAttempts, c.IsolationFailures, c.LimitRestarts)
	}
	b.WriteString("\n")

	section("Latency")
	b.WriteString("Wall time of every attempt of a run, failed ones included; nearest-rank percentiles. Preparation is reported apart.\n\n")
	b.WriteString("| condition | backend | p50 | p95 |\n|---|---|---|---|\n")
	for _, c := range r.Conditions {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Condition, c.Backend, seconds(c.P50MS), seconds(c.P95MS))
	}
	b.WriteString("\n")

	section("Usage")
	b.WriteString("Total model tokens per run, every attempt and model-based overhead once, in each provider's semantics (cached input never counted twice). " +
		"Reported, estimated and unavailable usage are kept apart; missing usage is not zero.\n\n")
	b.WriteString("| condition | backend | median total tokens (reported) | runs reported | runs without reported usage | estimated runs | median estimate | tokens per success |\n|---|---|---|---|---|---|---|---|\n")
	for _, c := range r.Conditions {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %s | %s |\n", c.Condition, c.Backend, num(c.MedianTokens), c.Measured, c.Unmeasured,
			c.Estimated, estimate(c.MedianEstimated), per(c.TokensPerSuccess, "%.0f", "run without reported usage"))
	}
	b.WriteString("\n")

	section("Cost")
	b.WriteString("Known spend and API-equivalent estimates per success, failures in the numerator. Subscription use is allowance, not a lower bill.\n\n")
	b.WriteString("| condition | backend | known spend per success | API-equivalent per success (estimate) |\n|---|---|---|---|\n")
	for _, c := range r.Conditions {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Condition, c.Backend, money(c.SpendPerSuccess, c.Recorded-c.Unsupported), money(c.APIEquivalent, c.Recorded-c.Unsupported))
	}
	b.WriteString("\n")

	section("Paired differences")
	fmt.Fprintf(&b, "Against %s, over tasks whose two cells are both complete; the median over tasks of the difference of the cells' median total tokens.\n\n", m.Adoption.Baseline)
	b.WriteString("| condition | backend | comparable tasks | median token difference | relative | success difference |\n|---|---|---|---|---|---|\n")
	for _, p := range r.Pairs {
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %+d |\n", p.Condition, p.Backend, p.Tasks, signed(p.MedianDiff), percent(p.MedianRelative), p.SuccessDiff)
	}
	b.WriteString("\n")

	section("Preparation overhead")
	b.WriteString("Deterministic indexing: no model tokens, but wall time and storage. First use is the build; amortized is the build over the runs observed to use it.\n\n")
	b.WriteString("| condition | backend | builds | median build (first use) | observed reuse per build | median amortized per run | refreshes | median refresh | median index size |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, p := range r.Prep {
		var counts []string
		for _, n := range p.ReuseCounts {
			counts = append(counts, fmt.Sprint(n))
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s | %d | %s | %s |\n", p.Condition, p.Backend, p.Builds, seconds(p.MedianBuildMS),
			dash(strings.Join(counts, ", ")), seconds(p.MedianAmortizedMS), p.Refreshes, seconds(p.MedianRefreshMS), bytesText(p.MedianIndexBytes))
	}
	b.WriteString("\n")

	section("Activity")
	b.WriteString("Tool calls and source reads where the transcript shows them.\n\n")
	b.WriteString("| condition | backend | observable runs | median tool calls | median source reads | median bytes read |\n|---|---|---|---|---|---|\n")
	for _, c := range r.Conditions {
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s |\n", c.Condition, c.Backend, c.Observable, num(c.MedianToolCalls), num(c.MedianReads), bytesText(c.MedianReadBytes))
	}
	b.WriteString("\n")

	section("Adoption rule")
	a := m.Adoption
	fmt.Fprintf(&b, "Pre-registered (issue #6), %s against %s: no new deterministic correctness or isolation failures; "+
		"equal or better success and at least %.0f%% lower median total model tokens, with complete comparable measured usage over at least %d tasks; "+
		"p50 latency, preparation amortized at the observed reuse, at most %.2f times the baseline's. "+
		"A pilot threshold, not statistical proof. A quality claim without token savings is argued separately and is not evaluated here.\n\n",
		a.Candidate, a.Baseline, 100*a.MinTokenReduction, a.MinTasks, a.MaxLatencyRatio)
	for _, v := range r.Adoption {
		outcome := v.Outcome
		if r.Synthetic {
			outcome += " (synthetic fixture: not a measurement)"
		}
		fmt.Fprintf(&b, "Backend %s: **%s**\n\n", v.Backend, outcome)
		for _, reason := range v.Reasons {
			fmt.Fprintf(&b, "- %s\n", reason)
		}
		b.WriteString("\n")
	}

	section("Reproduce")
	mp, rd := r.ManifestPath, r.RecordsDir
	if mp == "" {
		mp = "<manifest>"
	}
	if rd == "" {
		rd = "<records>"
	}
	fmt.Fprintf(&b, "```\ngo run ./bench/cmd/adamap-bench validate --manifest %s\n"+
		"go run ./bench/cmd/adamap-bench run --manifest %s --dry-run\n"+
		"go run ./bench/cmd/adamap-bench run --manifest %s --records %s --i-have-approval-for-N-runs %d\n"+
		"go run ./bench/cmd/adamap-bench reduce --manifest %s --records %s\n```\n\n",
		mp, mp, mp, rd, m.PlannedRuns(), mp, rd)
	banner()
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func seconds(ms float64) string {
	if math.IsNaN(ms) {
		return "-"
	}
	return fmt.Sprintf("%.1fs", ms/1000)
}

func num(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.0f", x)
}

func estimate(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.0f (estimate)", x)
}

func signed(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%+.0f", x)
}

func percent(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", 100*x)
}

func bytesText(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.0f B", x)
}

// per renders a per-success ratio: undefined with no successes, incomplete with missing amounts.
func per(r Ratio, format, missing string) string {
	switch {
	case r.Undefined:
		return "undefined (no successes)"
	case r.Incomplete == 1:
		return "incomplete (1 " + missing + ")"
	case r.Incomplete > 1:
		return fmt.Sprintf("incomplete (%d %s)", r.Incomplete, plural(missing))
	}
	return fmt.Sprintf(format, r.Value)
}

func money(r Ratio, runs int) string {
	if runs > 0 && r.Incomplete == runs {
		return "none known"
	}
	return per(r, "$%.4f", "run without a known amount")
}

// plural makes "run without X" into "runs without X".
func plural(s string) string {
	head, tail, _ := strings.Cut(s, " ")
	return head + "s " + tail
}
