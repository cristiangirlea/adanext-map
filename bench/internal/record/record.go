// Package record is one benchmark run's result record and the checks that hold it to its plan:
// the source and index it ran on, every attempt with its provider-reported usage, the outputs of
// its checks and their hashes, and what it was seen to query and read. A record never carries a
// success flag: Success derives it from the check outputs.
package record

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Format is the record format this package writes and reads.
const Format = 1

// Usage sources: reported by the provider, estimated (a tokenizer, bytes/4), or unavailable.
// Missing is not zero: an unavailable usage carries no counters at all.
const (
	Reported    = "reported"
	Estimated   = "estimated"
	Unavailable = "unavailable"
)

// Usage semantics differ by provider. Anthropic reports input without the cache (creation and
// reads are their own counters); OpenAI (Codex) and Google report input with the cached part
// already inside it, so adding cache reads to it would count them twice.
const (
	InputExcludesCache = "input-excludes-cache"
	InputIncludesCache = "input-includes-cache"
)

// SemanticsFor is the usage semantics of a backend CLI.
func SemanticsFor(cli string) string {
	switch cli {
	case "codex", "gemini":
		return InputIncludesCache
	default: // claude, and the fake backend that stands in for it
		return InputExcludesCache
	}
}

// Record is one run.
type Record struct {
	Format    int    `json:"format"`
	RunID     string `json:"run_id"`
	Task      string `json:"task"`
	Condition string `json:"condition"`
	Backend   string `json:"backend"`
	Repeat    int    `json:"repeat"`
	// Synthetic is copied from the manifest; a synthetic record is never a measurement.
	Synthetic bool   `json:"synthetic"`
	Manifest  string `json:"manifest"`
	StartedAt string `json:"started_at,omitempty"`
	// Source is the workspace before the worker started; After is the tree the checks ran on.
	Source Fingerprint `json:"source"`
	After  Fingerprint `json:"after"`
	// WorkerHead is the commit the worker left HEAD at when it moved it (a commit of its own);
	// the runner put HEAD back on the task's revision, keeping the tree, before the checks.
	WorkerHead string `json:"worker_head,omitempty"`
	Index      *Index `json:"index,omitempty"`
	// Unsupported says why the condition could not run on this host (its probe failed); such a
	// run has no attempts and is a coverage gap, never a success.
	Unsupported string `json:"unsupported,omitempty"`
	// LimitRestarts is how many times the run was started afresh after meeting the plan's usage
	// limit (each time with no record, its work directory gone); the attempts are the last start's.
	LimitRestarts int       `json:"limit_restarts,omitempty"`
	Attempts      []Attempt `json:"attempts"`
	// PlanMeter is the last reading of each plan window (five_hour, seven_day, ...) that the CLI's
	// meter (Claude Code's rate_limit_event) gave during the run's attempts.
	PlanMeter map[string]PlanWindow `json:"plan_meter,omitempty"`
	// Answer is the final attempt's final answer, as the adapter read it.
	Answer       string        `json:"answer,omitempty"`
	AnswerSHA256 string        `json:"answer_sha256,omitempty"`
	Checks       []CheckResult `json:"checks"`
	Isolation    Isolation     `json:"isolation"`
	// Overhead is model-based work outside the attempts (preparation, a handover), counted in the
	// whole-workflow totals once.
	Overhead []Overhead `json:"overhead,omitempty"`
}

// Fingerprint is a tree's identity: the commit, and the hash of everything that differs from it
// (CleanContent for a clean tree). A commit alone does not identify a dirty tree.
type Fingerprint struct {
	Commit  string `json:"commit"`
	Content string `json:"content"`
}

// Index is the condition's preparation as it ran: what it was built from, its digest, and what it cost.
type Index struct {
	Digest   string            `json:"digest"`
	Commit   string            `json:"commit"`
	Content  string            `json:"content"`
	Settings map[string]string `json:"settings,omitempty"`
	Reused   bool              `json:"reused"`
	// BuildMS is the wall time of the build; nil on a reuse.
	BuildMS *int64 `json:"build_ms,omitempty"`
	Bytes   int64  `json:"bytes"`
	// Refreshes are rebuilds after source edits during the run, where observed.
	Refreshes []Refresh `json:"refreshes,omitempty"`
}

// Refresh is one rebuild of the index after a source edit.
type Refresh struct {
	MS int64 `json:"ms"`
}

// PlanWindow is one reading of a plan window: the share of it used, and when it resets (Unix
// seconds; 0 when the meter did not say).
type PlanWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resets_at,omitempty"`
}

// Attempt is one try at the run. Failed attempts stay in the record and in every total.
type Attempt struct {
	N        int    `json:"n"`
	WallMS   int64  `json:"wall_ms"`
	Exit     int    `json:"exit"`
	TimedOut bool   `json:"timed_out,omitempty"`
	Error    string `json:"error,omitempty"`
	// SessionID is the CLI's own session or run ID, where it reports one.
	SessionID   string   `json:"session_id,omitempty"`
	Usage       Usage    `json:"usage"`
	Cost        Cost     `json:"cost"`
	Observed    Observed `json:"observed"`
	OutputBytes int64    `json:"output_bytes"`
	// TranscriptSHA256 binds the attempt to the transcript kept beside the record.
	TranscriptSHA256 string `json:"transcript_sha256,omitempty"`
}

// Completed reports whether the attempt ran to its end: exit 0, no error, no timeout.
func (a Attempt) Completed() bool { return a.Exit == 0 && a.Error == "" && !a.TimedOut }

// Usage is one attempt's model usage, kept with its semantics. Every counter is a pointer, so a
// counter the provider did not report stays missing instead of becoming zero.
type Usage struct {
	Source    string `json:"source"`
	Semantics string `json:"semantics,omitempty"`
	// Input is as the provider reports it: without the cache for input-excludes-cache, with the
	// cached reads inside it for input-includes-cache.
	Input         *int64 `json:"input,omitempty"`
	CacheCreation *int64 `json:"cache_creation,omitempty"`
	CacheRead     *int64 `json:"cache_read,omitempty"`
	Output        *int64 `json:"output,omitempty"`
	// Total is what the provider reported as the total, if it did; it must agree with the counters.
	Total *int64 `json:"total,omitempty"`
	// LastTurnContext is the context size of the last turn, kept apart from cumulative cache reads.
	LastTurnContext *int64 `json:"last_turn_context,omitempty"`
	// Estimator names how an estimate was made ("bytes/4", "tokenizer:<name>").
	Estimator string `json:"estimator,omitempty"`
}

// Cost is one attempt's money. Subscription use, known spend and API-equivalent estimates are kept
// apart; an estimate names its price date, model and context tier.
type Cost struct {
	Source string `json:"source"` // reported, estimated or unavailable
	// Kind is "spend" (money the provider billed), "api-equivalent" (what the tokens would cost
	// at API prices) or "subscription" (allowance only, no per-run money).
	Kind        string   `json:"kind,omitempty"`
	USD         *float64 `json:"usd,omitempty"`
	PriceDate   string   `json:"price_date,omitempty"`
	Model       string   `json:"model,omitempty"`
	ContextTier string   `json:"context_tier,omitempty"`
}

// Observed is the worker's activity where the transcript shows it.
type Observed struct {
	Observable bool           `json:"observable"`
	ToolCalls  map[string]int `json:"tool_calls,omitempty"`
	// SourceReads and SourceBytes count the file reads the transcript shows; nil when it does not.
	SourceReads *int   `json:"source_reads,omitempty"`
	SourceBytes *int64 `json:"source_bytes,omitempty"`
	// InjectedBytes is the size of what the condition injected into the prompt (the map).
	InjectedBytes int64 `json:"injected_bytes,omitempty"`
}

// CheckResult is one declared check as it ran.
type CheckResult struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Exit is the command's exit code (command and fix-tests).
	Exit         *int   `json:"exit,omitempty"`
	Output       string `json:"output"`
	OutputSHA256 string `json:"output_sha256"`
	// Content is the tree the check ran on; it must be the record's After.
	Content string `json:"content"`
	// Overlay is the fix revision whose test files a fix-tests check laid over the tree.
	Overlay string `json:"overlay,omitempty"`
	Passed  bool   `json:"passed"`
	// Reviewer and ReviewedOn are set when a human reviewed the answer (review checks only).
	Reviewer   string `json:"reviewer,omitempty"`
	ReviewedOn string `json:"reviewed_on,omitempty"`
}

// Isolation is whether the run stayed inside its workspace and away from what was hidden.
type Isolation struct {
	Checked    bool     `json:"checked"`
	Violations []string `json:"violations,omitempty"`
}

// Overhead is model usage outside the attempts.
type Overhead struct {
	What  string `json:"what"`
	Usage Usage  `json:"usage"`
}

// SHA256 is the hex SHA-256 of s.
func SHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Entry is one path that differs from the commit, as git status lists it, with the hash of its
// content ("" for a deleted file).
type Entry struct {
	Status string
	Path   string
	SHA256 string
}

// ContentHash is the hash of a tree's differences from its commit. The entries are sorted, so
// the order git lists them in does not matter.
func ContentHash(entries []Entry) string {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var b strings.Builder
	b.WriteString("adamap-bench content v1\n")
	for _, e := range sorted {
		fmt.Fprintf(&b, "%s\x00%s\x00%s\n", e.Status, e.Path, e.SHA256)
	}
	return SHA256(b.String())
}

// CleanContent is the content hash of a tree with no differences from its commit.
var CleanContent = ContentHash(nil)

// Write saves r as <dir>/<run id>.json.
func Write(dir string, r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, r.RunID+".json"), append(data, '\n'), 0o644)
}

// ReadDir reads every *.json record in dir, in name order.
func ReadDir(dir string) ([]Record, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Record
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		out = append(out, r)
	}
	return out, nil
}
