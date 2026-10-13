package runner_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
	"github.com/cristiangirlea/adanext-map/bench/internal/runner"
)

func cell(m *manifest.Manifest, condition string) runner.Cell {
	c := runner.Cell{Task: m.Tasks[0], Backend: m.Backends[0], Execution: m.Execution,
		Workspace: "/work/run/ws", StateDir: "/work/run/state", Attempt: 1}
	for _, x := range m.Conditions {
		if x.ID == condition {
			c.Condition = x
		}
	}
	c.Run = manifest.Run{ID: manifest.RunID(c.Task.ID, condition, c.Backend.ID, 1)}
	return c
}

func flag(argv []string, name string) string {
	i := slices.Index(argv, name)
	if i < 0 || i+1 >= len(argv) {
		return ""
	}
	return argv[i+1]
}

func TestClaudeCommandHoldsTheCellFixed(t *testing.T) {
	m := benchtest.Pilot()
	m.Backends[0].Settings = map[string]string{"alwaysThinkingEnabled": "false"}
	for _, cond := range []string{"none", "lsp", "adamap"} {
		inv, err := runner.Claude{}.Command(cell(m, cond))
		if err != nil {
			t.Fatal(err)
		}
		a := inv.Argv
		if a[0] != "claude" || !slices.Contains(a, "-p") || flag(a, "--output-format") != "stream-json" || !slices.Contains(a, "--verbose") {
			t.Fatalf("%s: argv %q", cond, a)
		}
		if flag(a, "--model") != "claude-sonnet-5-5" || flag(a, "--permission-mode") != "acceptEdits" || !slices.Contains(a, "--strict-mcp-config") {
			t.Fatalf("%s: argv %q", cond, a)
		}
		if inv.Stdin != m.Tasks[0].Prompt || inv.Dir != "/work/run/ws" || inv.Timeout.Seconds() != 900 {
			t.Fatalf("%s: invocation %+v", cond, inv)
		}
		if strings.Contains(strings.Join(a, " "), m.Tasks[0].Prompt) {
			t.Fatalf("%s: the prompt is on the command line", cond)
		}
		allowed := strings.Split(flag(a, "--allowedTools"), ",")
		for _, tool := range m.Execution.Tools {
			if !slices.Contains(allowed, tool) {
				t.Fatalf("%s: %s not allowed: %q", cond, tool, allowed)
			}
		}
		denied := strings.Split(flag(a, "--disallowedTools"), ",")
		var mcp struct {
			MCPServers map[string]struct {
				Type, Command string
				Args          []string
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(inv.Files[flag(a, "--mcp-config")]), &mcp); err != nil {
			t.Fatalf("%s: mcp config %q: %v", cond, inv.Files, err)
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inv.Files[flag(a, "--settings")]), &settings); err != nil || settings["alwaysThinkingEnabled"] != false {
			t.Fatalf("%s: settings %v %v", cond, settings, err)
		}
		switch cond {
		case "none":
			if !slices.Contains(denied, "LSP") || slices.Contains(allowed, "LSP") || len(mcp.MCPServers) != 0 {
				t.Fatalf("none: denied %q allowed %q servers %v", denied, allowed, mcp.MCPServers)
			}
		case "lsp":
			if !slices.Contains(allowed, "LSP") || len(mcp.MCPServers) != 0 {
				t.Fatalf("lsp: allowed %q", allowed)
			}
		case "adamap":
			s, ok := mcp.MCPServers["adamap"]
			if !ok || s.Type != "stdio" || s.Command != "adamap" || !slices.Equal(s.Args, []string{"mcp"}) || !slices.Contains(allowed, "mcp__adamap") {
				t.Fatalf("adamap: servers %+v allowed %q", mcp.MCPServers, allowed)
			}
		}
		for _, f := range inv.Files {
			if strings.Contains(f, "API_KEY") {
				t.Fatalf("%s: a config file names an API key", cond)
			}
		}
	}
	c := cell(m, "adamap")
	c.Injected = "the map"
	inv, _ := runner.Claude{}.Command(c)
	if flag(inv.Argv, "--append-system-prompt") != "the map" {
		t.Fatalf("injection: %q", inv.Argv)
	}
}

// A synthetic transcript in Claude Code's stream-json shape, written for this test.
const syntheticTranscript = `{"type":"system","subtype":"init","session_id":"s-1","model":"claude-sonnet-5-5"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"a.go"}},{"type":"tool_use","id":"g1","name":"Grep","input":{"pattern":"x"}}],"usage":{"input_tokens":10,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"output_tokens":5}}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":"package a\n"},{"type":"tool_result","tool_use_id":"g1","content":"a.go:1"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r2","name":"Read","input":{"file_path":"b.go"}}],"usage":{"input_tokens":12,"cache_creation_input_tokens":20,"cache_read_input_tokens":100,"output_tokens":6}}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r2","content":[{"type":"text","text":"package b"}]}]}}
{"type":"result","subtype":"success","is_error":false,"result":"pkg/a.go","session_id":"s-1","total_cost_usd":0.0123,"usage":{"input_tokens":22,"cache_creation_input_tokens":120,"cache_read_input_tokens":100,"output_tokens":11}}
`

func TestClaudeParseKeepsUsageSemanticsAndActivity(t *testing.T) {
	b := benchtest.Pilot().Backends[0]
	o := runner.Claude{}.Parse([]byte(syntheticTranscript), b)
	if o.Err != "" || o.Answer != "pkg/a.go" || o.SessionID != "s-1" {
		t.Fatalf("outcome %+v", o)
	}
	u := o.Usage
	if u.Source != record.Reported || u.Semantics != record.InputExcludesCache || *u.Input != 22 || *u.CacheCreation != 120 ||
		*u.CacheRead != 100 || *u.Output != 11 || u.Total != nil {
		t.Fatalf("usage %+v", u)
	}
	if p := u.Problems(record.InputExcludesCache); len(p) != 0 {
		t.Fatal(p)
	}
	// The last turn's context (12 + 20 + 100) is kept apart from the cumulative cache reads.
	if u.LastTurnContext == nil || *u.LastTurnContext != 132 {
		t.Fatalf("last turn context %v", u.LastTurnContext)
	}
	if o.Cost.Source != record.Reported || o.Cost.Kind != "api-equivalent" || *o.Cost.USD != 0.0123 || o.Cost.Model != b.Model {
		t.Fatalf("cost %+v", o.Cost)
	}
	obs := o.Observed
	if !obs.Observable || obs.ToolCalls["Read"] != 2 || obs.ToolCalls["Grep"] != 1 || *obs.SourceReads != 2 || *obs.SourceBytes != int64(len("package a\n")+len("package b")) {
		t.Fatalf("observed %+v reads %v bytes %v", obs, *obs.SourceReads, *obs.SourceBytes)
	}
}

// Missing is not zero: a transcript without a result, or a result without every counter, gives
// unavailable usage, never zeros.
func TestClaudeParseWithoutAResultHasNoUsage(t *testing.T) {
	b := benchtest.Pilot().Backends[0]
	lines := strings.Split(syntheticTranscript, "\n")
	o := runner.Claude{}.Parse([]byte(strings.Join(lines[:5], "\n")), b)
	if o.Err == "" || o.Usage.Source != record.Unavailable || o.Cost.Source != record.Unavailable || o.Usage.Input != nil {
		t.Fatalf("outcome %+v", o)
	}
	partial := `{"type":"result","subtype":"success","is_error":false,"result":"x","usage":{"input_tokens":3,"output_tokens":1}}`
	o = runner.Claude{}.Parse([]byte(partial), b)
	if o.Usage.Source != record.Unavailable || o.Err != "" {
		t.Fatalf("partial counters: %+v", o)
	}
	failed := `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"","usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}`
	o = runner.Claude{}.Parse([]byte(failed), b)
	if !strings.Contains(o.Err, "error_max_turns") || o.Usage.Source != record.Reported {
		t.Fatalf("a failed result keeps its usage and says why: %+v", o)
	}
}
