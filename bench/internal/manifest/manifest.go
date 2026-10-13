// Package manifest is the benchmark's experiment manifest (issue #6): the five conditions with
// their pinned identities, the backends that run them, the tasks with their checks, provenance and
// leakage controls, the repeats, the order, the cache treatment, the budget and the pre-registered
// adoption rule. bench/README.md documents the format; Validate says what is wrong with one.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Format is the manifest and tasks-file format this package reads.
const Format = 1

// The five condition kinds of issue #6.
const (
	KindNone      = "none"      // no navigation tool beyond the host's own non-navigation tools
	KindHostLSP   = "host-lsp"  // the host's built-in LSP
	KindSerena    = "serena"    // Serena over MCP
	KindCodeGraph = "codegraph" // CodeGraph over MCP
	KindAdamap    = "adamap"    // adamap over MCP, the map injected, or both
)

// ConditionKinds lists the kinds in the order reports print them.
var ConditionKinds = []string{KindNone, KindHostLSP, KindSerena, KindCodeGraph, KindAdamap}

// The task kinds.
const (
	TaskNavigation = "navigation"
	TaskImpact     = "impact"
	TaskFix        = "fix"
	TaskControl    = "control" // a task a code map is unlikely to help with
)

// TaskKinds lists the task kinds.
var TaskKinds = []string{TaskNavigation, TaskImpact, TaskFix, TaskControl}

// The check types. Review is a human-reviewed factual answer: recorded with its reviewer, never an
// executable gate.
const (
	CheckCommand   = "command"    // a command run in the workspace after the worker; passes on the expected exit
	CheckFixTests  = "fix-tests"  // the fix revision's test files laid over the workspace, then a command
	CheckAnswer    = "answer"     // the worker's final answer holds every expected string
	CheckNoChanges = "no-changes" // the worker left the tree as it found it
	CheckReview    = "review"     // a human reviews the answer against the criteria
)

// Manifest is one experiment.
type Manifest struct {
	Format int    `json:"format"`
	Name   string `json:"name"`
	// Scale is "pilot" (the full design: five conditions, at least ten tasks, three repeats) or
	// "smoke" (a wiring check, labelled as one in every output).
	Scale string `json:"scale"`
	// Synthetic marks a fixture: its records are labelled synthetic everywhere and never become
	// measurements. A synthetic manifest runs only the fake backend, and the fake backend runs only
	// in a synthetic manifest.
	Synthetic  bool        `json:"synthetic,omitempty"`
	Conditions []Condition `json:"conditions"`
	Backends   []Backend   `json:"backends"`
	// Tasks are listed here, or read from TasksFile (relative to the manifest), not both.
	Tasks     []Task    `json:"tasks,omitempty"`
	TasksFile string    `json:"tasks_file,omitempty"`
	Repeats   int       `json:"repeats"`
	Order     Order     `json:"order"`
	Cache     string    `json:"cache"` // "cold": every run builds its own index; "warm": the first repeat builds, the rest reuse it
	Execution Execution `json:"execution"`
	Budget    Budget    `json:"budget"`
	Adoption  Adoption  `json:"adoption"`
}

// Condition is one of the five ways a worker gets (or does not get) code navigation.
type Condition struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
	// Components are the pinned identities: tool, indexer, schema, language servers.
	Components []Component `json:"components,omitempty"`
	// Mode separates the map's injection from on-demand queries for adamap: "queries", "map" or
	// "map+queries". Empty for the other kinds.
	Mode string `json:"mode,omitempty"`
	// MCP is the server the condition adds to the worker, if any.
	MCP *Server `json:"mcp,omitempty"`
	// Index is the deterministic preparation the condition needs before a run, if any.
	Index *Index `json:"index,omitempty"`
	// Inject is a command whose output is appended to the worker's system prompt (the map).
	Inject []string `json:"inject,omitempty"`
	// Tools are the host tools the condition adds to the fixed set (the LSP tool for host-lsp, the
	// MCP server's tools); Deny are the ones it takes away (the LSP tool everywhere else).
	Tools []string `json:"tools,omitempty"`
	Deny  []string `json:"deny,omitempty"`
	// Probe is a command that must succeed for the condition to be usable on this host; when it
	// fails, the run is recorded as unsupported, never as a baseline success.
	Probe    []string          `json:"probe,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// Component is one pinned identity.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Source says where that version comes from (a module path, a release URL, a commit).
	Source string `json:"source,omitempty"`
}

// Server is an MCP server started by the host over stdio.
type Server struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Index is a condition's preparation: a command run in the workspace and the path it writes.
type Index struct {
	Command  []string          `json:"command"`
	Output   string            `json:"output"` // relative to the workspace; hashed into the index digest
	Settings map[string]string `json:"settings,omitempty"`
}

// Backend is a coding CLI on its subscription plan, with the model and effort it is held at.
type Backend struct {
	ID       string            `json:"id"`
	CLI      string            `json:"cli"` // "claude", "codex", "gemini", or "fake" in a synthetic manifest
	Version  string            `json:"version"`
	Model    string            `json:"model"`
	Effort   string            `json:"effort,omitempty"`
	Plan     string            `json:"plan"` // always "subscription"
	Settings map[string]string `json:"settings,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// Order is how conditions are counterbalanced within each block of runs.
type Order struct {
	Scheme string `json:"scheme"` // "latin-square"
	Seed   uint64 `json:"seed"`
}

// Execution is what stays fixed across every cell.
type Execution struct {
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxAttempts    int      `json:"max_attempts"` // the retry policy: a failed attempt is retried up to this many in all
	PermissionMode string   `json:"permission_mode"`
	Tools          []string `json:"tools"` // the host tools every condition has
	// ContextEdit is the provider context-edit setting, recorded and held fixed (Harness#116 owns it).
	ContextEdit         string `json:"context_edit,omitempty"`
	CheckTimeoutSeconds int    `json:"check_timeout_seconds,omitempty"`
}

// Budget bounds the experiment.
type Budget struct {
	MaxRuns int `json:"max_runs"`
}

// Adoption is the pre-registered adamap-versus-no-tool rule of issue #6.
type Adoption struct {
	Candidate string `json:"candidate"` // the adamap condition's ID
	Baseline  string `json:"baseline"`  // the no-tool condition's ID
	// MinTokenReduction is the least relative drop in median total model tokens, at least 0.10.
	MinTokenReduction float64 `json:"min_token_reduction"`
	// MaxLatencyRatio is the acceptable latency trade-off: the candidate's p50 wall time, its
	// amortized preparation included, over the baseline's.
	MaxLatencyRatio float64 `json:"max_latency_ratio"`
	// MinTasks is the least number of tasks with complete comparable cells for a verdict.
	MinTasks int `json:"min_tasks"`
}

// TasksFile is the file a manifest's tasks_file names.
type TasksFile struct {
	Format int    `json:"format"`
	Tasks  []Task `json:"tasks"`
}

// Task is one held-out task.
type Task struct {
	ID string `json:"id"`
	// Status is "candidate" until the owner approves the task; only approved tasks run.
	Status     string     `json:"status"`
	Kind       string     `json:"kind"`
	Repository Repository `json:"repository"`
	// Revision is the full pre-fix commit the workspace is checked out at.
	Revision string `json:"revision"`
	Prompt   string `json:"prompt"`
	// Requires says what the task actually needs (toolchains, network, platform).
	Requires   []string   `json:"requires,omitempty"`
	Checks     []Check    `json:"checks"`
	Provenance Provenance `json:"provenance"`
	Leakage    Leakage    `json:"leakage"`
}

// Repository is where a task's source comes from.
type Repository struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Visibility string `json:"visibility"` // "public" or "private"
}

// Check is one deterministic check (or a review), declared before execution.
type Check struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Command []string `json:"command,omitempty"`
	// Needs says what the check needs to run: toolchains, the fix revision, the final answer.
	Needs  []string `json:"needs,omitempty"`
	Expect Expect   `json:"expect,omitempty"`
	// Paths are the fix revision's files a fix-tests check lays over the workspace, as path.Match
	// patterns on the slash path or its base name; default *_test.go.
	Paths []string `json:"paths,omitempty"`
	// Criteria is what a reviewer checks the answer against (review only).
	Criteria string `json:"criteria,omitempty"`
}

// Expect is what a check passes on.
type Expect struct {
	Exit     *int     `json:"exit,omitempty"`     // command and fix-tests: the exit code, default 0
	Contains []string `json:"contains,omitempty"` // answer: every string must appear
}

// Provenance is where a task came from.
type Provenance struct {
	Source string `json:"source"` // "issue", "pr" or "authored"
	Ref    string `json:"ref"`    // owner/repo#n, or a short description for an authored task
	Title  string `json:"title,omitempty"`
	// FixRevision is the accepted solution's commit; graders read it, workers never see it.
	FixRevision string `json:"fix_revision,omitempty"`
	SelectedBy  string `json:"selected_by"`
	SelectedOn  string `json:"selected_on"` // YYYY-MM-DD
}

// Leakage is what must not be visible to the worker.
type Leakage struct {
	// HideFuture: the workspace holds the pre-fix revision only, no later commit or ref.
	HideFuture bool `json:"hide_future"`
	// HiddenRevisions must not be reachable from the workspace (the fix revision at least).
	HiddenRevisions []string `json:"hidden_revisions,omitempty"`
	// HiddenPaths must not exist in the workspace (tests that reveal the solution).
	HiddenPaths []string `json:"hidden_paths,omitempty"`
	// HideLabels: issue labels and discussion stay out of the prompt and the context.
	HideLabels bool   `json:"hide_labels"`
	Notes      string `json:"notes,omitempty"`
}

// Load reads a manifest and, when it names one, its tasks file. It does not validate.
func Load(path string) (*Manifest, error) {
	var m Manifest
	if err := readJSON(path, &m); err != nil {
		return nil, err
	}
	if m.TasksFile != "" {
		if len(m.Tasks) > 0 {
			return nil, fmt.Errorf("%s: tasks and tasks_file are both set", path)
		}
		tasks, err := LoadTasks(filepath.Join(filepath.Dir(path), filepath.FromSlash(m.TasksFile)))
		if err != nil {
			return nil, err
		}
		m.Tasks = tasks.Tasks
	}
	return &m, nil
}

// LoadTasks reads a tasks file.
func LoadTasks(path string) (*TasksFile, error) {
	var f TasksFile
	if err := readJSON(path, &f); err != nil {
		return nil, err
	}
	if f.Format != Format {
		return nil, fmt.Errorf("%s: format %d, want %d", path, f.Format, Format)
	}
	return &f, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := decodeStrict(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// decodeStrict refuses unknown fields, so a misspelt key is an error rather than a silent default.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("text after the JSON value")
	}
	return nil
}
