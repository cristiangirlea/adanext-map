package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

func TestAValidPilotHasNoIssues(t *testing.T) {
	for name, m := range map[string]*manifest.Manifest{"pilot": benchtest.Pilot(), "synthetic": benchtest.Synthetic()} {
		if issues := m.Validate(); len(issues) != 0 {
			t.Errorf("%s: %v", name, issues)
		}
	}
}

// Each case breaks a valid pilot one way and names the issue it must raise: where, what, and
// whether it only blocks a run (the plan can still be printed) or is structural.
func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(m *manifest.Manifest)
		path    string
		msg     string
		blocker bool
	}{
		{"a condition without an id", func(m *manifest.Manifest) { m.Conditions[1].ID = "" }, "conditions[1].id", "missing", false},
		{"a duplicate condition id", func(m *manifest.Manifest) { m.Conditions[2].ID = "lsp" }, "conditions[2].id", `duplicate "lsp"`, false},
		{"a missing condition kind", func(m *manifest.Manifest) { m.Conditions = m.Conditions[:4] }, "conditions", `no "adamap" condition`, false},
		{"a kind twice", func(m *manifest.Manifest) { m.Conditions[3].Kind = manifest.KindSerena }, "conditions[3].kind", `"serena" again`, false},
		{"an unknown kind", func(m *manifest.Manifest) { m.Conditions[0].Kind = "grep" }, "conditions[0].kind", "unknown", false},
		{"a task without an id", func(m *manifest.Manifest) { m.Tasks[4].ID = "" }, "tasks[4].id", "missing", false},
		{"a duplicate task id", func(m *manifest.Manifest) { m.Tasks[5].ID = m.Tasks[1].ID }, "tasks[5].id", `duplicate "t01"`, false},
		{"a duplicate backend id", func(m *manifest.Manifest) { m.Backends = append(m.Backends, m.Backends[0]) }, "backends[1].id", "duplicate", false},
		// An ID in two namespaces would let a record swap its task and condition unnoticed.
		{"a task id that is a condition id", func(m *manifest.Manifest) { m.Tasks[0].ID = "serena" }, "tasks[0].id", "also a condition id", false},
		{"a backend id that is a task id", func(m *manifest.Manifest) { m.Backends[0].ID = "t03" }, "backends[0].id", "also a task id", false},
		{"an id that cannot be part of a run id", func(m *manifest.Manifest) { m.Tasks[0].ID = "a/b" }, "tasks[0].id", "lower-case letters", false},
		{"a task without checks", func(m *manifest.Manifest) { m.Tasks[2].Checks = nil }, "tasks[2].checks", "no checks", false},
		{"a task with only a review", func(m *manifest.Manifest) {
			m.Tasks[0].Checks = []manifest.Check{{ID: "r", Type: manifest.CheckReview, Criteria: "names the file"}}
		}, "tasks[0].checks", "no executable check", false},
		{"a fix task without a test or command", func(m *manifest.Manifest) {
			m.Tasks[2].Checks = []manifest.Check{{ID: "a", Type: manifest.CheckAnswer, Expect: manifest.Expect{Contains: []string{"x"}}}}
		}, "tasks[2].checks", "a fix task needs a command or fix-tests check", false},
		{"a duplicate check id", func(m *manifest.Manifest) { m.Tasks[3].Checks[1].ID = "answer" }, "tasks[3].checks[1].id", "duplicate", false},
		{"an answer check with nothing to find", func(m *manifest.Manifest) { m.Tasks[0].Checks[0].Expect.Contains = nil }, "tasks[0].checks[0].expect.contains", "missing", false},
		{"a command check without a command", func(m *manifest.Manifest) { m.Tasks[2].Checks[0].Command = nil }, "tasks[2].checks[0].command", "missing", false},
		{"fix-tests without a fix revision", func(m *manifest.Manifest) { m.Tasks[2].Provenance.FixRevision = "" }, "tasks[2].checks[0]", "fix-tests needs provenance.fix_revision", false},
		{"a task without provenance", func(m *manifest.Manifest) { m.Tasks[0].Provenance = manifest.Provenance{} }, "tasks[0].provenance.source", "missing", false},
		{"provenance without a selection date", func(m *manifest.Manifest) { m.Tasks[0].Provenance.SelectedOn = "13 Oct" }, "tasks[0].provenance.selected_on", "YYYY-MM-DD", false},
		{"a short revision", func(m *manifest.Manifest) { m.Tasks[0].Revision = "abc1234" }, "tasks[0].revision", "full commit hash", false},
		{"the fix revision as the task's revision", func(m *manifest.Manifest) { m.Tasks[2].Revision = m.Tasks[2].Provenance.FixRevision },
			"tasks[2].revision", "is the fix revision", false},
		{"a fix revision not hidden", func(m *manifest.Manifest) { m.Tasks[2].Leakage.HiddenRevisions = nil }, "tasks[2].leakage.hidden_revisions", "must hide the fix revision", false},
		{"future commits visible", func(m *manifest.Manifest) { m.Tasks[0].Leakage.HideFuture = false }, "tasks[0].leakage.hide_future", "must be true", false},
		{"labels visible", func(m *manifest.Manifest) { m.Tasks[0].Leakage.HideLabels = false }, "tasks[0].leakage.hide_labels", "must be true", false},
		{"the prompt naming the fix", func(m *manifest.Manifest) { m.Tasks[2].Prompt += " See " + m.Tasks[2].Provenance.FixRevision[:10] },
			"tasks[2].prompt", "names the fix revision", false},
		{"an unpinned component", func(m *manifest.Manifest) { m.Conditions[2].Components[0].Version = "latest" }, "conditions[2].components[0].version", "not pinned", true},
		{"a version range", func(m *manifest.Manifest) { m.Conditions[4].Components[1].Version = "^0.2" }, "conditions[4].components[1].version", "not pinned", true},
		{"a placeholder version", func(m *manifest.Manifest) { m.Conditions[3].Components[0].Version = "UNPINNED" }, "conditions[3].components[0].version", "not pinned", true},
		{"a tool condition without components", func(m *manifest.Manifest) { m.Conditions[3].Components = nil }, "conditions[3].components", "no pinned identity", true},
		{"an unpinned CLI", func(m *manifest.Manifest) { m.Backends[0].Version = "" }, "backends[0].version", "not pinned", true},
		{"a model alias", func(m *manifest.Manifest) { m.Backends[0].Model = "sonnet" }, "backends[0].model", "not pinned", true},
		{"a candidate task", func(m *manifest.Manifest) { m.Tasks[1].Status = "candidate" }, "tasks[1].status", "not approved", true},
		{"a budget short of the plan", func(m *manifest.Manifest) { m.Budget.MaxRuns = 149 }, "budget.max_runs", "149 is less than the plan's 150 runs", true},
		{"a budget short with two backends", func(m *manifest.Manifest) {
			b := m.Backends[0]
			b.ID = "claude2"
			m.Backends = append(m.Backends, b)
		}, "budget.max_runs", "less than the plan's 300 runs", true},
		{"no repeats", func(m *manifest.Manifest) { m.Repeats = 0 }, "repeats", "at least 1", false},
		{"a pilot with too few repeats", func(m *manifest.Manifest) { m.Repeats = 2; m.Budget.MaxRuns = 100 }, "repeats", "a pilot needs at least 3", false},
		{"a pilot with too few tasks", func(m *manifest.Manifest) { m.Tasks = m.Tasks[:9]; m.Budget.MaxRuns = 135 }, "tasks", "a pilot needs at least 10", false},
		{"a pilot without a control task", func(m *manifest.Manifest) {
			for i := range m.Tasks {
				if m.Tasks[i].Kind == manifest.TaskControl {
					m.Tasks[i] = benchtest.Task(i, manifest.TaskNavigation)
				}
			}
		}, "tasks", `no "control" task`, false},
		{"an unknown order", func(m *manifest.Manifest) { m.Order.Scheme = "random" }, "order.scheme", "unknown", false},
		{"an unknown cache treatment", func(m *manifest.Manifest) { m.Cache = "hot" }, "cache", "unknown", false},
		{"no timeout", func(m *manifest.Manifest) { m.Execution.TimeoutSeconds = 0 }, "execution.timeout_seconds", "at least 1", false},
		{"no attempts", func(m *manifest.Manifest) { m.Execution.MaxAttempts = 0 }, "execution.max_attempts", "at least 1", false},
		{"a token threshold below the issue's", func(m *manifest.Manifest) { m.Adoption.MinTokenReduction = 0.05 }, "adoption.min_token_reduction", "at least 0.10", false},
		{"an adoption candidate that is not adamap", func(m *manifest.Manifest) { m.Adoption.Candidate = "serena" }, "adoption.candidate", `"adamap" condition`, false},
		{"an adoption baseline that is not none", func(m *manifest.Manifest) { m.Adoption.Baseline = "lsp" }, "adoption.baseline", `"none" condition`, false},
		{"a fake backend in a real manifest", func(m *manifest.Manifest) { m.Backends[0].CLI = "fake" }, "backends[0].cli", "only in a synthetic manifest", false},
		{"a real backend in a synthetic manifest", func(m *manifest.Manifest) { m.Synthetic = true }, "backends[0].cli", "a synthetic manifest runs only the fake backend", false},
		{"a smoke with an unknown scale", func(m *manifest.Manifest) { m.Scale = "full" }, "scale", "unknown", false},
	}
	for _, c := range cases {
		m := benchtest.Pilot()
		c.mutate(m)
		issues := m.Validate()
		found := false
		for _, i := range issues {
			if strings.HasPrefix(i.Path, c.path) && strings.Contains(i.Msg, c.msg) {
				found = true
				if i.Blocker != c.blocker {
					t.Errorf("%s: %v has blocker %v, want %v", c.name, i, i.Blocker, c.blocker)
				}
			}
		}
		if !found {
			t.Errorf("%s: no issue at %s saying %q; got %v", c.name, c.path, c.msg, issues)
		}
	}
}

// A provider API key anywhere in the configuration is refused: the benchmark runs CLIs on their
// subscription plans and never moves to paid API access.
func TestValidateRefusesAnyAPIKeyConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m *manifest.Manifest)
		path   string
	}{
		{"backend env", func(m *manifest.Manifest) { m.Backends[0].Env = map[string]string{"ANTHROPIC_API_KEY": "x"} }, "backends[0].env.ANTHROPIC_API_KEY"},
		{"backend env auth token", func(m *manifest.Manifest) { m.Backends[0].Env = map[string]string{"ANTHROPIC_AUTH_TOKEN": "x"} }, "backends[0].env.ANTHROPIC_AUTH_TOKEN"},
		{"any _API_KEY", func(m *manifest.Manifest) { m.Conditions[2].Env = map[string]string{"SOME_VENDOR_API_KEY": ""} }, "conditions[2].env.SOME_VENDOR_API_KEY"},
		{"MCP server env", func(m *manifest.Manifest) { m.Conditions[4].MCP.Env = map[string]string{"OPENAI_API_KEY": "x"} }, "conditions[4].mcp.env.OPENAI_API_KEY"},
		{"cloud routing", func(m *manifest.Manifest) { m.Backends[0].Env = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"} }, "backends[0].env.CLAUDE_CODE_USE_BEDROCK"},
		{"an api key helper setting", func(m *manifest.Manifest) { m.Backends[0].Settings = map[string]string{"apiKeyHelper": "/bin/key"} }, "backends[0].settings.apiKeyHelper"},
		{"an api key flag", func(m *manifest.Manifest) { m.Conditions[3].MCP.Args = []string{"mcp", "--api-key=abc"} }, "conditions[3].mcp.args[1]"},
		{"an api plan", func(m *manifest.Manifest) { m.Backends[0].Plan = "api" }, "backends[0].plan"},
		// A setting's value is written as JSON, so a key can hide inside it.
		{"a key inside a setting's value", func(m *manifest.Manifest) {
			m.Backends[0].Settings = map[string]string{"env": `{"ANTHROPIC_API_KEY": "sk-abc"}`}
		}, "backends[0].settings.env.ANTHROPIC_API_KEY"},
		{"cloud routing inside a setting's value", func(m *manifest.Manifest) {
			m.Conditions[0].Settings = map[string]string{"env": `{"nested": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`}
		}, "conditions[0].settings.env.nested.CLAUDE_CODE_USE_BEDROCK"},
		{"a console login", func(m *manifest.Manifest) { m.Backends[0].Settings = map[string]string{"forceLoginMethod": "console"} }, "backends[0].settings.forceLoginMethod"},
	}
	for _, c := range cases {
		m := benchtest.Pilot()
		c.mutate(m)
		issues := m.Validate()
		found := false
		for _, i := range issues {
			if i.Path == c.path {
				found = true
				if i.Blocker {
					t.Errorf("%s: an API key is structural, not a blocker: %v", c.name, i)
				}
				if strings.Contains(i.Msg, "abc") {
					t.Errorf("%s: the message must not echo the value: %v", c.name, i)
				}
			}
		}
		if !found {
			t.Errorf("%s: no issue at %s; got %v", c.name, c.path, issues)
		}
	}
}

// The claude process gets the backend's and the condition's env, but Authorize reads the user
// settings from the runner's own: an env that moves the settings' home would let a manifest point
// the CLI at files the scan never read.
func TestValidateRefusesAnEnvThatMovesTheSettingsHome(t *testing.T) {
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA", "claude_config_dir"} {
		for _, site := range []struct {
			where  string
			mutate func(m *manifest.Manifest, env map[string]string)
		}{
			{"backends[0].env.", func(m *manifest.Manifest, env map[string]string) { m.Backends[0].Env = env }},
			{"conditions[1].env.", func(m *manifest.Manifest, env map[string]string) { m.Conditions[1].Env = env }},
			{"conditions[4].mcp.env.", func(m *manifest.Manifest, env map[string]string) { m.Conditions[4].MCP.Env = env }},
		} {
			m := benchtest.Pilot()
			site.mutate(m, map[string]string{name: "/elsewhere"})
			found := false
			for _, i := range m.Validate() {
				if i.Path == site.where+name {
					found = true
					if !strings.Contains(i.Msg, "where Claude Code reads its settings") || strings.Contains(i.Msg, "elsewhere") {
						t.Errorf("%s%s: message %q", site.where, name, i.Msg)
					}
				}
			}
			if !found {
				t.Errorf("%s%s: not refused", site.where, name)
			}
		}
	}
}

func TestPlannedRunsCountsEveryCell(t *testing.T) {
	m := benchtest.Pilot()
	if got := m.PlannedRuns(); got != 10*5*3 {
		t.Fatalf("PlannedRuns = %d, want 150", got)
	}
}

func TestLoadReadsTheTasksFileAndRefusesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("tasks.json", `{"format": 1, "tasks": [{"id": "a", "status": "candidate"}]}`)
	m, err := manifest.Load(write("m.json", `{"format": 1, "name": "x", "tasks_file": "tasks.json"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) != 1 || m.Tasks[0].ID != "a" {
		t.Fatalf("tasks %+v", m.Tasks)
	}
	if _, err := manifest.Load(write("bad.json", `{"format": 1, "nmae": "x"}`)); err == nil || !strings.Contains(err.Error(), "nmae") {
		t.Fatalf("a misspelt key: err %v", err)
	}
	write("t2.json", `{"format": 1, "tasks": []}`)
	if _, err := manifest.Load(write("both.json", `{"format": 1, "tasks_file": "t2.json", "tasks": [{"id": "b"}]}`)); err == nil {
		t.Fatal("tasks and tasks_file together: no error")
	}
}

func TestFixTestsPathsArePatterns(t *testing.T) {
	m := benchtest.Pilot()
	m.Tasks[2].Checks[0].Paths = []string{"*_test.go", "["}
	m.Tasks[0].Checks[0].Paths = []string{"*.go"}
	issues := m.Validate()
	if !benchtest.HasIssue(issues, "tasks[2].checks[0].paths[1]", "path.Match pattern") {
		t.Errorf("a bad pattern: %v", issues)
	}
	if benchtest.HasIssue(issues, "tasks[2].checks[0].paths[0]", "") {
		t.Errorf("a good pattern refused: %v", issues)
	}
	if !benchtest.HasIssue(issues, "tasks[0].checks[0].paths", "only a fix-tests check") {
		t.Errorf("paths on an answer check: %v", issues)
	}
}

// A run is refused for a variable that would bill (a provider key or a cloud route), not for
// every token on the machine.
func TestIsProviderKeyVar(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "anthropic_api_key", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"GOOGLE_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "SOME_VENDOR_API_KEY"} {
		if !manifest.IsProviderKeyVar(name) {
			t.Errorf("%s is not refused", name)
		}
	}
	for _, name := range []string{"GH_TOKEN", "NPM_AUTH_TOKEN", "PATH", "HOME", "API_KEYBOARD"} {
		if manifest.IsProviderKeyVar(name) {
			t.Errorf("%s is refused", name)
		}
	}
}

func TestASubscriptionLoginSettingIsAllowed(t *testing.T) {
	m := benchtest.Pilot()
	m.Backends[0].Settings = map[string]string{"forceLoginMethod": "claudeai", "env": `{"DISABLE_TELEMETRY": "1"}`}
	if issues := m.Validate(); len(issues) != 0 {
		t.Fatal(issues)
	}
}

// Paths from a manifest stay inside the workspace on every platform, and a repository URL is
// never read by git as an option.
func TestPathsStayInsideAndURLsAreNotOptions(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../x", "a/../../x", "/abs", "C:/x", `C:\x`, `\server\x`, ".git", ".git/x", "a:b"} {
		m := benchtest.Pilot()
		m.Conditions[4].Index.Output = bad
		if !benchtest.HasIssue(m.Validate(), "conditions[4].index.output", "inside the workspace") {
			t.Errorf("index output %q accepted", bad)
		}
	}
	for _, bad := range []string{"/etc/passwd", `C:\x`, "../x", "."} {
		m := benchtest.Pilot()
		m.Tasks[0].Leakage.HiddenPaths = []string{bad}
		if !benchtest.HasIssue(m.Validate(), "tasks[0].leakage.hidden_paths[0]", "inside the repository") {
			t.Errorf("hidden path %q accepted", bad)
		}
	}
	m := benchtest.Pilot()
	m.Conditions[4].Index.Output = "out/a..b"
	m.Tasks[0].Leakage.HiddenPaths = []string{"internal/x_test.go"}
	if issues := m.Validate(); len(issues) != 0 {
		t.Errorf("good paths refused: %v", issues)
	}
	m.Tasks[0].Repository.URL = "--upload-pack=touch x"
	if !benchtest.HasIssue(m.Validate(), "tasks[0].repository.url", "option") {
		t.Error("a URL that git would read as an option is accepted")
	}
}
