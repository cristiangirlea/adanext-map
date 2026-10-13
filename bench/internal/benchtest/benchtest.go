// Package benchtest builds the benchmark's test fixtures in code: a valid pilot manifest, its
// synthetic twin, and synthetic run records. Everything it makes for records is synthetic and says so.
package benchtest

import (
	"fmt"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// Rev is a full commit hash made from a seed, for fixtures.
func Rev(seed int) string { return fmt.Sprintf("%040x", seed+0xabc000) }

// Pilot is a valid, runnable pilot manifest: the five conditions pinned, one claude backend, ten
// approved tasks covering every kind, three repeats and a budget of exactly the plan.
func Pilot() *manifest.Manifest {
	m := &manifest.Manifest{
		Format: manifest.Format,
		Name:   "fixture-pilot",
		Scale:  "pilot",
		Conditions: []manifest.Condition{
			{ID: "none", Kind: manifest.KindNone, Deny: []string{"LSP"}},
			{ID: "lsp", Kind: manifest.KindHostLSP, Tools: []string{"LSP"},
				Components: []manifest.Component{{Name: "gopls", Version: "v0.20.0"}},
				Probe:      []string{"gopls", "version"}},
			{ID: "serena", Kind: manifest.KindSerena, Deny: []string{"LSP"},
				Components: []manifest.Component{{Name: "serena", Version: "0.1.4", Source: "git+https://github.com/oraios/serena@0123456789abcdef0123456789abcdef01234567"}},
				MCP:        &manifest.Server{Name: "serena", Command: "serena", Args: []string{"start-mcp-server"}}},
			{ID: "codegraph", Kind: manifest.KindCodeGraph, Deny: []string{"LSP"},
				Components: []manifest.Component{{Name: "codegraph", Version: "1.2.3"}},
				MCP:        &manifest.Server{Name: "codegraph", Command: "codegraph", Args: []string{"mcp"}},
				Index:      &manifest.Index{Command: []string{"codegraph", "index", "."}, Output: ".codegraph"}},
			{ID: "adamap", Kind: manifest.KindAdamap, Mode: "queries", Deny: []string{"LSP"},
				Components: []manifest.Component{{Name: "adamap", Version: "0.0.1-dev"}, {Name: "scip-go", Version: "v0.2.7"}},
				MCP:        &manifest.Server{Name: "adamap", Command: "adamap", Args: []string{"mcp"}},
				Index:      &manifest.Index{Command: []string{"adamap", "index"}, Output: ".adamap"}},
		},
		Backends: []manifest.Backend{{ID: "claude", CLI: "claude", Version: "2.1.0", Model: "claude-sonnet-5-5",
			Effort: "medium", Plan: "subscription"}},
		Repeats:   3,
		Order:     manifest.Order{Scheme: "latin-square", Seed: 6},
		Cache:     "warm",
		Execution: manifest.Execution{TimeoutSeconds: 900, MaxAttempts: 2, PermissionMode: "acceptEdits", Tools: []string{"Read", "Grep", "Glob", "Edit", "Bash"}},
		Budget:    manifest.Budget{MaxRuns: 150},
		Adoption:  manifest.Adoption{Candidate: "adamap", Baseline: "none", MinTokenReduction: 0.10, MaxLatencyRatio: 1.25, MinTasks: 8},
	}
	kinds := []string{manifest.TaskNavigation, manifest.TaskImpact, manifest.TaskFix, manifest.TaskControl}
	for i := range 10 {
		m.Tasks = append(m.Tasks, Task(i, kinds[i%len(kinds)]))
	}
	return m
}

// Task is an approved task of the given kind with checks that fit it.
func Task(i int, kind string) manifest.Task {
	t := manifest.Task{
		ID:         fmt.Sprintf("t%02d", i),
		Status:     "approved",
		Kind:       kind,
		Repository: manifest.Repository{Name: "example/repo", URL: "https://example.com/repo.git", Visibility: "public"},
		Revision:   Rev(i),
		Prompt:     fmt.Sprintf("Task %d: answer or fix as asked.", i),
		Requires:   []string{"go"},
		Provenance: manifest.Provenance{Source: "authored", Ref: fmt.Sprintf("fixture task %d", i), SelectedBy: "fixture", SelectedOn: "2026-10-13"},
		Leakage:    manifest.Leakage{HideFuture: true, HideLabels: true},
	}
	switch kind {
	case manifest.TaskFix:
		fix := Rev(100 + i)
		t.Provenance = manifest.Provenance{Source: "issue", Ref: fmt.Sprintf("example/repo#%d", i), FixRevision: fix, SelectedBy: "fixture", SelectedOn: "2026-10-13"}
		t.Leakage.HiddenRevisions = []string{fix}
		t.Checks = []manifest.Check{{ID: "tests", Type: manifest.CheckFixTests, Command: []string{"go", "test", "./..."}, Needs: []string{"go", "fix_revision"}}}
	case manifest.TaskControl:
		t.Checks = []manifest.Check{{ID: "answer", Type: manifest.CheckAnswer, Needs: []string{"final_answer"}, Expect: manifest.Expect{Contains: []string{"MIT"}}},
			{ID: "untouched", Type: manifest.CheckNoChanges}}
	default:
		t.Checks = []manifest.Check{{ID: "answer", Type: manifest.CheckAnswer, Needs: []string{"final_answer"}, Expect: manifest.Expect{Contains: []string{"pkg/a.go", "pkg/b.go"}}}}
	}
	return t
}

// Synthetic is Pilot as a synthetic fixture: the fake backend, labelled synthetic.
func Synthetic() *manifest.Manifest {
	m := Pilot()
	m.Name = "fixture-synthetic"
	m.Synthetic = true
	m.Backends = []manifest.Backend{{ID: "fake", CLI: "fake", Version: "1", Model: "fake-model-1", Plan: "subscription"}}
	return m
}

// HasIssue reports whether issues holds one whose path starts with path and whose message holds msg.
func HasIssue(issues []manifest.Issue, path, msg string) bool {
	for _, i := range issues {
		if strings.HasPrefix(i.Path, path) && strings.Contains(i.Msg, msg) {
			return true
		}
	}
	return false
}
