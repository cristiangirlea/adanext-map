package manifest

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Issue is one thing wrong with a manifest. Path names the field (conditions[2].components[0].version).
// A Blocker stops a run but not a plan: an unpinned identity, a candidate task, a budget short of
// the plan. Every other issue is structural and stops both.
type Issue struct {
	Path    string
	Msg     string
	Blocker bool
}

func (i Issue) String() string { return i.Path + ": " + i.Msg }

// PlannedRuns is the number of runs the plan holds: tasks x conditions x backends x repeats.
func (m *Manifest) PlannedRuns() int {
	return len(m.Tasks) * len(m.Conditions) * len(m.Backends) * m.Repeats
}

// Pilot minimums from issue #6.
const (
	pilotMinTasks     = 10
	pilotMinRepeats   = 3
	minTokenReduction = 0.10
)

var (
	idPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	hexCommit  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	floatingRe = regexp.MustCompile(`[*^~<>|\s]|\.x$|^x$`)
	// floating are versions and model names that move under the experiment.
	floating = []string{"", "latest", "stable", "main", "master", "head", "nightly", "next", "default",
		"any", "unpinned", "todo", "tbd", "pin-me", "auto", "best"}
	// modelAliases are model names a CLI resolves to whatever is current.
	modelAliases = []string{"opus", "sonnet", "haiku", "fable", "opusplan", "gpt", "gemini", "pro", "flash"}
)

// Validate lists every issue with m, in field order.
func (m *Manifest) Validate() []Issue {
	v := &validator{}
	if m.Format != Format {
		v.add("format", fmt.Sprintf("%d, want %d", m.Format, Format))
	}
	if m.Name == "" {
		v.add("name", "missing")
	}
	if m.Scale != "pilot" && m.Scale != "smoke" {
		v.add("scale", fmt.Sprintf("unknown %q: pilot or smoke", m.Scale))
	}
	v.conditions(m)
	v.backends(m)
	v.tasks(m)
	v.crossIDs(m)
	if m.Repeats < 1 {
		v.add("repeats", "at least 1")
	} else if m.Scale == "pilot" && m.Repeats < pilotMinRepeats {
		v.add("repeats", fmt.Sprintf("%d: a pilot needs at least %d; label a smaller run a smoke", m.Repeats, pilotMinRepeats))
	}
	if m.Order.Scheme != "latin-square" {
		v.add("order.scheme", fmt.Sprintf("unknown %q: latin-square", m.Order.Scheme))
	}
	if m.Cache != "cold" && m.Cache != "warm" {
		v.add("cache", fmt.Sprintf("unknown %q: cold or warm", m.Cache))
	}
	e := m.Execution
	if e.TimeoutSeconds < 1 {
		v.add("execution.timeout_seconds", "at least 1")
	}
	if e.MaxAttempts < 1 {
		v.add("execution.max_attempts", "at least 1")
	}
	if e.PermissionMode == "" {
		v.add("execution.permission_mode", "missing: permissions are fixed across cells")
	}
	if len(e.Tools) == 0 {
		v.add("execution.tools", "missing: the fixed tool set every condition has")
	}
	if e.CheckTimeoutSeconds < 0 {
		v.add("execution.check_timeout_seconds", "negative")
	}
	if want := m.PlannedRuns(); m.Budget.MaxRuns < want {
		v.block("budget.max_runs", fmt.Sprintf("%d is less than the plan's %d runs (tasks x conditions x backends x repeats)", m.Budget.MaxRuns, want))
	}
	v.adoption(m)
	return v.issues
}

type validator struct{ issues []Issue }

func (v *validator) add(path, msg string) { v.issues = append(v.issues, Issue{Path: path, Msg: msg}) }
func (v *validator) block(path, msg string) {
	v.issues = append(v.issues, Issue{Path: path, Msg: msg, Blocker: true})
}

// id checks one ID and records it in seen; it reports a duplicate against the earlier index.
func (v *validator) id(path, id string, seen map[string]int, at int) {
	switch {
	case id == "":
		v.add(path, "missing")
	case !idPattern.MatchString(id):
		v.add(path, fmt.Sprintf("%q: lower-case letters, digits, - and _ only, so it can be part of a run id", id))
	default:
		if j, ok := seen[id]; ok {
			v.add(path, fmt.Sprintf("duplicate %q (also at index %d)", id, j))
			return
		}
		seen[id] = at
	}
}

func (v *validator) conditions(m *Manifest) {
	seen := map[string]int{}
	kinds := map[string]int{}
	for i, c := range m.Conditions {
		p := fmt.Sprintf("conditions[%d]", i)
		v.id(p+".id", c.ID, seen, i)
		if !slices.Contains(ConditionKinds, c.Kind) {
			v.add(p+".kind", fmt.Sprintf("unknown %q: one of %s", c.Kind, strings.Join(ConditionKinds, ", ")))
		} else if j, ok := kinds[c.Kind]; ok {
			v.add(p+".kind", fmt.Sprintf("%q again (conditions[%d]): one condition per kind", c.Kind, j))
		} else {
			kinds[c.Kind] = i
		}
		if c.Kind != KindNone && len(c.Components) == 0 {
			v.block(p+".components", "no pinned identity: name the tool, indexer and versions")
		}
		for j, comp := range c.Components {
			cp := fmt.Sprintf("%s.components[%d]", p, j)
			if comp.Name == "" {
				v.add(cp+".name", "missing")
			}
			v.pinned(cp+".version", comp.Version)
		}
		switch c.Kind {
		case KindAdamap:
			if c.Mode != "queries" && c.Mode != "map" && c.Mode != "map+queries" {
				v.add(p+".mode", fmt.Sprintf("unknown %q: queries, map or map+queries", c.Mode))
			}
			if c.Mode != "queries" && len(c.Inject) == 0 {
				v.add(p+".inject", "mode "+c.Mode+" needs the command that prints the map")
			}
		default:
			if c.Mode != "" {
				v.add(p+".mode", "only the adamap condition has a mode")
			}
		}
		if c.MCP != nil {
			if c.MCP.Name == "" || !idPattern.MatchString(c.MCP.Name) {
				v.add(p+".mcp.name", fmt.Sprintf("%q: lower-case letters, digits, - and _", c.MCP.Name))
			}
			if c.MCP.Command == "" {
				v.add(p+".mcp.command", "missing")
			}
			v.noKeysInArgs(p+".mcp.args", c.MCP.Args)
			v.noKeys(p+".mcp.env", c.MCP.Env)
			v.noSettingsHome(p+".mcp.env", c.MCP.Env)
		}
		if c.Index != nil {
			if len(c.Index.Command) == 0 {
				v.add(p+".index.command", "missing")
			}
			if out := path.Clean(c.Index.Output); !Inside(c.Index.Output) || out == ".git" || strings.HasPrefix(out, ".git/") {
				v.add(p+".index.output", fmt.Sprintf("%q: a path inside the workspace, outside .git", c.Index.Output))
			}
			v.noKeysInArgs(p+".index.command", c.Index.Command)
			v.noKeys(p+".index.settings", c.Index.Settings)
		}
		v.noKeysInArgs(p+".inject", c.Inject)
		v.noKeysInArgs(p+".probe", c.Probe)
		v.noKeys(p+".settings", c.Settings)
		v.noKeys(p+".env", c.Env)
		v.noSettingsHome(p+".env", c.Env)
	}
	if m.Scale == "pilot" || len(m.Conditions) == 0 {
		for _, k := range ConditionKinds {
			if _, ok := kinds[k]; !ok {
				v.add("conditions", fmt.Sprintf("no %q condition: a pilot has all five", k))
			}
		}
	}
}

func (v *validator) backends(m *Manifest) {
	if len(m.Backends) == 0 {
		v.add("backends", "missing")
	}
	seen := map[string]int{}
	for i, b := range m.Backends {
		p := fmt.Sprintf("backends[%d]", i)
		v.id(p+".id", b.ID, seen, i)
		switch {
		case b.CLI == "fake" && !m.Synthetic:
			v.add(p+".cli", "the fake backend runs only in a synthetic manifest")
		case b.CLI != "fake" && m.Synthetic:
			v.add(p+".cli", fmt.Sprintf("%q: a synthetic manifest runs only the fake backend", b.CLI))
		case !slices.Contains([]string{"claude", "codex", "gemini", "fake"}, b.CLI):
			v.add(p+".cli", fmt.Sprintf("unknown %q: claude, codex or gemini", b.CLI))
		}
		v.pinned(p+".version", b.Version)
		v.pinned(p+".model", b.Model)
		if slices.Contains(modelAliases, strings.ToLower(b.Model)) {
			v.block(p+".model", fmt.Sprintf("%q is not pinned: an alias moves to the current model; name the full model id", b.Model))
		}
		if b.Plan != "subscription" {
			v.add(p+".plan", fmt.Sprintf("%q: the benchmark runs CLIs on their subscription plans, never API access", b.Plan))
		}
		v.noKeys(p+".settings", b.Settings)
		v.noKeys(p+".env", b.Env)
		v.noSettingsHome(p+".env", b.Env)
	}
}

func (v *validator) tasks(m *Manifest) {
	if len(m.Tasks) == 0 {
		v.add("tasks", "missing")
		return
	}
	seen := map[string]int{}
	kinds := map[string]bool{}
	for i, t := range m.Tasks {
		p := fmt.Sprintf("tasks[%d]", i)
		v.id(p+".id", t.ID, seen, i)
		v.task(p, t)
		kinds[t.Kind] = true
	}
	if m.Scale == "pilot" {
		if len(m.Tasks) < pilotMinTasks {
			v.add("tasks", fmt.Sprintf("%d: a pilot needs at least %d; label a smaller run a smoke", len(m.Tasks), pilotMinTasks))
		}
		for _, k := range TaskKinds {
			if !kinds[k] {
				v.add("tasks", fmt.Sprintf("no %q task: a pilot includes navigation, impact, fix and control tasks", k))
			}
		}
	}
}

// ValidateTasks lists the issues of a tasks file on its own: each task, and duplicate IDs.
func ValidateTasks(f *TasksFile) []Issue {
	v := &validator{}
	seen := map[string]int{}
	for i, t := range f.Tasks {
		p := fmt.Sprintf("tasks[%d]", i)
		v.id(p+".id", t.ID, seen, i)
		v.task(p, t)
	}
	return v.issues
}

func (v *validator) task(p string, t Task) {
	switch t.Status {
	case "approved":
	case "candidate":
		v.block(p+".status", "candidate: not approved by the owner yet")
	default:
		v.add(p+".status", fmt.Sprintf("unknown %q: candidate or approved", t.Status))
	}
	if !slices.Contains(TaskKinds, t.Kind) {
		v.add(p+".kind", fmt.Sprintf("unknown %q: one of %s", t.Kind, strings.Join(TaskKinds, ", ")))
	}
	if t.Repository.Name == "" {
		v.add(p+".repository.name", "missing")
	}
	if t.Repository.URL == "" {
		v.add(p+".repository.url", "missing")
	} else if strings.HasPrefix(t.Repository.URL, "-") {
		v.add(p+".repository.url", "starts with -, which git would read as an option")
	}
	if t.Repository.Visibility != "public" && t.Repository.Visibility != "private" {
		v.add(p+".repository.visibility", fmt.Sprintf("unknown %q: public or private", t.Repository.Visibility))
	}
	if !hexCommit.MatchString(t.Revision) {
		v.add(p+".revision", fmt.Sprintf("%q: the full commit hash of the pre-fix revision", t.Revision))
	}
	if strings.TrimSpace(t.Prompt) == "" {
		v.add(p+".prompt", "missing")
	}
	v.provenance(p, t)
	v.leakage(p, t)
	v.checks(p, t)
}

func (v *validator) provenance(p string, t Task) {
	pr := t.Provenance
	pp := p + ".provenance"
	if !slices.Contains([]string{"issue", "pr", "authored"}, pr.Source) {
		v.add(pp+".source", fmt.Sprintf("missing or unknown %q: issue, pr or authored", pr.Source))
	}
	if pr.Ref == "" {
		v.add(pp+".ref", "missing")
	}
	if pr.SelectedBy == "" {
		v.add(pp+".selected_by", "missing")
	}
	if _, err := time.Parse(time.DateOnly, pr.SelectedOn); err != nil {
		v.add(pp+".selected_on", fmt.Sprintf("%q: a date as YYYY-MM-DD", pr.SelectedOn))
	}
	if pr.FixRevision == "" {
		if t.Kind == TaskFix && pr.Source != "authored" {
			v.add(pp+".fix_revision", "missing: a fix taken from an issue or PR names the accepted solution's commit")
		}
		return
	}
	if !hexCommit.MatchString(pr.FixRevision) {
		v.add(pp+".fix_revision", fmt.Sprintf("%q: a full commit hash", pr.FixRevision))
		return
	}
	if pr.FixRevision == t.Revision {
		v.add(p+".revision", "is the fix revision: the workspace must start before the fix")
	}
	if strings.Contains(t.Prompt, pr.FixRevision[:7]) {
		v.add(p+".prompt", "names the fix revision: the worker must not see the solution")
	}
}

func (v *validator) leakage(p string, t Task) {
	l := t.Leakage
	lp := p + ".leakage"
	if !l.HideFuture {
		v.add(lp+".hide_future", "must be true: the workspace holds the pre-fix revision and nothing later")
	}
	if !l.HideLabels {
		v.add(lp+".hide_labels", "must be true: labels and discussion stay out of the worker's context")
	}
	for j, r := range l.HiddenRevisions {
		if !hexCommit.MatchString(r) {
			v.add(fmt.Sprintf("%s.hidden_revisions[%d]", lp, j), fmt.Sprintf("%q: a full commit hash", r))
		}
	}
	if fix := t.Provenance.FixRevision; fix != "" && !slices.Contains(l.HiddenRevisions, fix) {
		v.add(lp+".hidden_revisions", "must hide the fix revision "+fix)
	}
	for j, hp := range l.HiddenPaths {
		if !Inside(hp) {
			v.add(fmt.Sprintf("%s.hidden_paths[%d]", lp, j), fmt.Sprintf("%q: a path inside the repository", hp))
		}
	}
}

func (v *validator) checks(p string, t Task) {
	if len(t.Checks) == 0 {
		v.add(p+".checks", "no checks: declare the deterministic checks before execution")
		return
	}
	seen := map[string]int{}
	gates := 0
	runs := false
	for j, c := range t.Checks {
		cp := fmt.Sprintf("%s.checks[%d]", p, j)
		v.id(cp+".id", c.ID, seen, j)
		switch c.Type {
		case CheckCommand, CheckFixTests:
			gates++
			runs = true
			if len(c.Command) == 0 {
				v.add(cp+".command", "missing")
			}
			v.noKeysInArgs(cp+".command", c.Command)
			if c.Type == CheckFixTests && t.Provenance.FixRevision == "" {
				v.add(cp, "fix-tests needs provenance.fix_revision, whose test files it lays over the workspace")
			}
			if len(c.Expect.Contains) > 0 {
				v.add(cp+".expect.contains", "only an answer check matches text")
			}
			for k, pat := range c.Paths {
				if _, err := path.Match(pat, ""); err != nil || pat == "" {
					v.add(fmt.Sprintf("%s.paths[%d]", cp, k), fmt.Sprintf("%q: a path.Match pattern", pat))
				}
			}
		case CheckAnswer:
			gates++
			if len(c.Expect.Contains) == 0 {
				v.add(cp+".expect.contains", "missing: the strings the answer must hold")
			}
			for k, s := range c.Expect.Contains {
				if strings.TrimSpace(s) == "" {
					v.add(fmt.Sprintf("%s.expect.contains[%d]", cp, k), "empty")
				}
			}
		case CheckNoChanges:
			gates++
		case CheckReview:
			if c.Criteria == "" {
				v.add(cp+".criteria", "missing: what the reviewer checks the answer against")
			}
		default:
			v.add(cp+".type", fmt.Sprintf("unknown %q", c.Type))
		}
		if len(c.Paths) > 0 && c.Type != CheckFixTests {
			v.add(cp+".paths", "only a fix-tests check lays files over the workspace")
		}
		if c.Expect.Exit != nil && c.Type != CheckCommand && c.Type != CheckFixTests {
			v.add(cp+".expect.exit", "only a command or fix-tests check has an exit code")
		}
	}
	if gates == 0 {
		v.add(p+".checks", "no executable check: a review alone is not a gate")
	}
	if t.Kind == TaskFix && !runs {
		v.add(p+".checks", "a fix task needs a command or fix-tests check")
	}
}

// crossIDs refuses an ID used in two namespaces, so a record that swaps its task and condition
// (or backend) names a run that does not exist rather than another real one.
func (v *validator) crossIDs(m *Manifest) {
	type owner struct{ kind, path string }
	first := map[string]owner{}
	note := func(kind, path, id string) {
		if id == "" {
			return
		}
		if o, ok := first[id]; ok && o.kind != kind {
			v.add(path, fmt.Sprintf("%q is also a %s id (%s): ids must be unique across conditions, tasks and backends", id, o.kind, o.path))
			return
		}
		if _, ok := first[id]; !ok {
			first[id] = owner{kind, path}
		}
	}
	for i, c := range m.Conditions {
		note("condition", fmt.Sprintf("conditions[%d].id", i), c.ID)
	}
	for i, t := range m.Tasks {
		note("task", fmt.Sprintf("tasks[%d].id", i), t.ID)
	}
	for i, b := range m.Backends {
		note("backend", fmt.Sprintf("backends[%d].id", i), b.ID)
	}
}

func (v *validator) adoption(m *Manifest) {
	a := m.Adoption
	kindOf := map[string]string{}
	for _, c := range m.Conditions {
		kindOf[c.ID] = c.Kind
	}
	if kindOf[a.Candidate] != KindAdamap {
		v.add("adoption.candidate", fmt.Sprintf("%q: the rule compares the \"adamap\" condition", a.Candidate))
	}
	if kindOf[a.Baseline] != KindNone {
		v.add("adoption.baseline", fmt.Sprintf("%q: the rule's baseline is the \"none\" condition", a.Baseline))
	}
	if a.MinTokenReduction < minTokenReduction {
		v.add("adoption.min_token_reduction", fmt.Sprintf("%v: at least 0.10, as issue #6 pre-registers", a.MinTokenReduction))
	}
	if a.MaxLatencyRatio <= 0 {
		v.add("adoption.max_latency_ratio", "missing: the acceptable latency trade-off, registered before the runs")
	}
	if a.MinTasks < 1 {
		v.add("adoption.min_tasks", "at least 1")
	}
}

// pinned blocks a version or model that is empty, a floating word, a range or a wildcard.
func (v *validator) pinned(path, s string) {
	if slices.Contains(floating, strings.ToLower(s)) || floatingRe.MatchString(s) {
		v.block(path, fmt.Sprintf("%q is not pinned: name an exact version", s))
	}
}

// API-key detection. The values are never echoed: only the name says what was refused.
var (
	apiKeyNames = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_API_KEY", "OPENAI_API_KEY",
		"CODEX_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_API_KEY", "AZURE_OPENAI_API_KEY",
		"OPENROUTER_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY", "GOOGLE_GENAI_USE_VERTEXAI", "GOOGLE_APPLICATION_CREDENTIALS",
		// A gateway, and the cloud routes' endpoints, credentials and auth bypasses.
		"ANTHROPIC_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_VERTEX_PROJECT_ID",
		"ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "AWS_PROFILE", "CLAUDE_CODE_SKIP_BEDROCK_AUTH", "CLAUDE_CODE_SKIP_VERTEX_AUTH",
		"CLAUDE_CODE_SKIP_FOUNDRY_AUTH"}
	apiKeyish = regexp.MustCompile(`(?i)(api[_-]?key|auth[_-]?token|apikeyhelper)`)
)

// IsProviderKeyVar reports whether an environment variable, when set, hands a provider API key
// (or a pay-per-token cloud route) to a CLI: the named provider variables and any *_API_KEY.
// Unlike IsAPIKeyName it leaves unrelated tokens (GH_TOKEN, NPM_AUTH_TOKEN) alone, so a real run
// is refused for what would bill, not for every credential on the machine.
func IsProviderKeyVar(name string) bool {
	u := strings.ToUpper(name)
	return slices.Contains(apiKeyNames, u) || strings.HasSuffix(u, "_API_KEY")
}

// IsAPIKeyName reports whether an environment variable or setting name would hand a provider API
// key (or a pay-per-token cloud route) to a CLI.
func IsAPIKeyName(name string) bool {
	return slices.Contains(apiKeyNames, strings.ToUpper(name)) || apiKeyish.MatchString(name)
}

func (v *validator) noKeys(path string, kv map[string]string) {
	names := make([]string, 0, len(kv))
	for k := range kv {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v.noKey(path+"."+k, k)
		if k == "forceLoginMethod" && kv[k] != "claudeai" {
			v.add(path+"."+k, "a login other than claudeai is API billing: the benchmark runs on subscription plans")
		}
		// A value that reads as JSON is written as that JSON (runner.settingValue), so the names
		// inside it count too: {"env": {"ANTHROPIC_API_KEY": ...}} is a key.
		var x any
		if json.Unmarshal([]byte(kv[k]), &x) == nil {
			v.noKeysIn(path+"."+k, x)
		}
	}
}

// settingsHomeNames are the variables that move where a CLI looks for its user settings. Authorize
// scans the runner's own, so an env that moved them would send the CLI to files it never read.
var settingsHomeNames = []string{"CLAUDE_CONFIG_DIR", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"}

func (v *validator) noSettingsHome(path string, kv map[string]string) {
	names := make([]string, 0, len(kv))
	for k := range kv {
		if slices.Contains(settingsHomeNames, strings.ToUpper(k)) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		v.add(path+"."+k, "changes where Claude Code reads its settings: the user settings the run is authorized on must be the ones that were scanned")
	}
}

func (v *validator) noKey(path, name string) {
	if IsAPIKeyName(name) {
		v.add(path, "configures provider API access: the benchmark refuses API keys and runs on subscription plans")
	}
}

func (v *validator) noKeysIn(path string, x any) {
	switch x := x.(type) {
	case map[string]any:
		names := make([]string, 0, len(x))
		for k := range x {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v.noKey(path+"."+k, k)
			v.noKeysIn(path+"."+k, x[k])
		}
	case []any:
		for i, e := range x {
			v.noKeysIn(fmt.Sprintf("%s[%d]", path, i), e)
		}
	}
}

func (v *validator) noKeysInArgs(path string, args []string) {
	for i, a := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && IsAPIKeyName(name) {
			v.add(fmt.Sprintf("%s[%d]", path, i), "passes a provider API key: the benchmark refuses API keys and runs on subscription plans")
		}
	}
}

// Inside reports whether p, a slash path from a manifest, names something strictly inside the
// directory it is relative to, on every platform: not empty or the directory itself, not
// absolute, no drive, colon or backslash, no .. that leaves it.
func Inside(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\:`+"\x00") {
		return false
	}
	c := path.Clean(p)
	return c != "." && c != ".." && !strings.HasPrefix(c, "../")
}
