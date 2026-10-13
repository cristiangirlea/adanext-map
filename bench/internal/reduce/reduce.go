// Package reduce turns a checked record set into the benchmark's report, bench/RESULTS.md:
// coverage per cell, success, latency, usage and its completeness, paired differences over
// comparable cells only, cost per success with failures counted, preparation overhead first-use
// and amortized at the observed reuse, and the pre-registered adoption rule's verdict.
package reduce

import (
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// Report is the reduced result of one manifest's records.
type Report struct {
	Manifest *manifest.Manifest
	// ManifestPath and RecordsDir are what the reproduction commands name.
	ManifestPath, RecordsDir string
	Records                  int
	Synthetic                bool
	Smoke                    bool
	Cells                    []Cell
	Conditions               []Condition
	Pairs                    []Pair
	Prep                     []Prep
	Adoption                 []Verdict
}

// Cell is one task under one condition and backend, over its repeats.
type Cell struct {
	Task, Condition, Backend string
	Planned, Recorded        int
	Unsupported              int
	Measured                 int // runs whose usage is reported and complete on every attempt
	Successes                int
	// Complete: every planned repeat recorded, supported and measured.
	Complete bool
}

// Ratio is a per-success figure that can be undefined (no successes) or incomplete (a run
// without usage), never a number made up from missing data.
type Ratio struct {
	Value      float64
	Undefined  bool // zero successes
	Incomplete int  // runs whose amount is missing
}

// Condition is one condition under one backend, over every task.
type Condition struct {
	Condition, Backend, Kind string
	Planned, Recorded        int
	Unsupported              int
	Successes                int
	Attempts, FailedAttempts int
	IsolationFailures        int
	P50MS, P95MS             float64
	// MedianTokens is over measured runs only; Measured and Unmeasured say how many runs that is.
	MedianTokens         float64
	Measured, Unmeasured int
	// Estimated runs are kept apart from reported ones.
	Estimated                                     int
	MedianEstimated                               float64
	TokensPerSuccess                              Ratio
	SpendPerSuccess                               Ratio // money a provider billed
	APIEquivalent                                 Ratio // what the tokens would cost at API prices, an estimate
	Observable                                    int
	MedianToolCalls, MedianReads, MedianReadBytes float64
}

// Pair is a condition against the baseline over the tasks where both cells are complete.
type Pair struct {
	Condition, Baseline, Backend string
	Tasks                        int
	MedianDiff                   float64 // tokens, condition minus baseline, the median over tasks of the per-task median difference
	MedianRelative               float64 // the same relative to the baseline's per-task median
	SuccessDiff                  int
}

// Prep is a condition's preparation overhead: first use, and amortized at the reuse observed.
type Prep struct {
	Condition, Backend string
	Builds             int
	MedianBuildMS      float64
	// ReuseCounts are the observed runs per built index; Amortized is build time over them.
	ReuseCounts       []int
	MedianAmortizedMS float64
	Refreshes         int
	MedianRefreshMS   float64
	MedianIndexBytes  float64
}

// Verdict is the adoption rule's outcome for one backend: pass, fail or inconclusive.
type Verdict struct {
	Backend string
	Outcome string
	Reasons []string
}
