package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

// Cell is one run as an adapter sees it.
type Cell struct {
	Run       manifest.Run
	Task      manifest.Task
	Condition manifest.Condition
	Backend   manifest.Backend
	Execution manifest.Execution
	// Workspace is the fresh checkout the worker runs in; StateDir is outside it, for the
	// adapter's configuration files and the transcripts.
	Workspace, StateDir string
	// Injected is what the condition injects into the system prompt (the map), if anything.
	Injected string
	Attempt  int
}

// Invocation is one attempt's process: the program and arguments, where it runs, what it adds to
// the scrubbed environment, the prompt on stdin, the timeout, and the files written first. Watch,
// when set, sees each line of stdout as it comes; an error from it kills the process and stops
// the execution.
type Invocation struct {
	Argv    []string
	Dir     string
	Env     []string
	Stdin   string
	Timeout time.Duration
	Files   map[string]string
	Watch   func(line []byte) error
}

// Outcome is what an adapter reads from an attempt's output.
type Outcome struct {
	Answer    string
	SessionID string
	Usage     record.Usage
	Cost      record.Cost
	Observed  record.Observed
	// Err says why the attempt failed by the CLI's own account, when it did.
	Err string
	// Limit says the attempt ended at the plan's usage limit, or at a rate-limit or overload error
	// the CLI stopped retrying: not a task failure, so the run is not recorded.
	Limit string
}

// Adapter is one coding CLI.
type Adapter interface {
	// Command builds the attempt's invocation for a cell.
	Command(c Cell) (Invocation, error)
	// Parse reads the attempt's stdout.
	Parse(stdout []byte, b manifest.Backend) Outcome
}

// Claude is Claude Code's CLI in print mode with stream-json output. Each attempt is a fresh
// session; the settings are the user's (checked by Authorize) and the cell's, never the task
// repository's (--setting-sources user); the MCP servers are only the condition's
// (--strict-mcp-config); the tools are the fixed set plus the condition's; the prompt goes on stdin.
type Claude struct{}

func (Claude) Command(c Cell) (Invocation, error) {
	tools := slices.Concat(c.Execution.Tools, c.Condition.Tools)
	servers := map[string]any{}
	if s := c.Condition.MCP; s != nil {
		entry := map[string]any{"type": "stdio", "command": s.Command, "args": nonNil(s.Args)}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		servers[s.Name] = entry
		tools = append(tools, "mcp__"+s.Name)
	}
	mcp, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return Invocation{}, err
	}
	settings := map[string]any{}
	for _, kv := range []map[string]string{c.Backend.Settings, c.Condition.Settings} {
		for k, v := range kv {
			settings[k] = settingValue(v)
		}
	}
	set, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return Invocation{}, err
	}
	mcpPath := filepath.Join(c.StateDir, "mcp.json")
	setPath := filepath.Join(c.StateDir, "settings.json")
	argv := []string{"claude", "-p", "--output-format", "stream-json", "--verbose",
		"--model", c.Backend.Model, "--permission-mode", c.Execution.PermissionMode,
		"--setting-sources", "user", "--strict-mcp-config", "--mcp-config", mcpPath, "--settings", setPath,
		"--allowedTools", strings.Join(tools, ",")}
	if len(c.Condition.Deny) > 0 {
		argv = append(argv, "--disallowedTools", strings.Join(c.Condition.Deny, ","))
	}
	if c.Injected != "" {
		argv = append(argv, "--append-system-prompt", c.Injected)
	}
	return Invocation{
		Argv: argv, Dir: c.Workspace, Env: envList(c.Backend.Env, c.Condition.Env), Stdin: c.Task.Prompt,
		Timeout: time.Duration(c.Execution.TimeoutSeconds) * time.Second,
		Files:   map[string]string{mcpPath: string(mcp) + "\n", setPath: string(set) + "\n"},
		Watch:   subscriptionOnly(),
	}, nil
}

// subscriptionOnly watches a stream-json transcript for its init event and lets the session go on
// only when the init shows the subscription login (apiKeySource "none"). Any other source, a
// missing one, or a turn (or any event but a system or rate_limit_event one) before the init stops
// it; an init that never comes is bounded by the attempt's timeout. Before the init and after it,
// a rate_limit_event (the plan's meter) that shows overage in use stops it too. It is a backstop,
// not a gate: the CLI starts its first request right after writing the init, so at most one
// request can get through before the process is stopped.
func subscriptionOnly() func(line []byte) error {
	seen := false
	return func(line []byte) error {
		// After the init only the meter matters: skip the turns, which can be large, unread.
		if seen && !bytes.Contains(line, []byte(`"rate_limit_event"`)) {
			return nil
		}
		var e struct {
			Type, Subtype string
			APIKeySource  *string        `json:"apiKeySource"`
			Info          map[string]any `json:"rate_limit_info"`
		}
		if json.Unmarshal(line, &e) != nil {
			return nil
		}
		if e.Type == "rate_limit_event" {
			if field := overageInUse(e.Info, false, "rate_limit_info"); field != "" {
				return fmt.Errorf("the plan's meter shows %w at %s: the CLI was stopped", ErrOverage, field)
			}
		}
		if seen {
			return nil
		}
		switch {
		case e.Type == "system" && e.Subtype == "init":
			if e.APIKeySource == nil {
				return fmt.Errorf("the CLI's init event gives no apiKeySource, so its login is unknown: refused as possible API billing")
			}
			if *e.APIKeySource != "none" {
				return fmt.Errorf("the CLI's init event gives apiKeySource %q, not the subscription login (\"none\"): refused as API billing", *e.APIKeySource)
			}
			seen = true
			return nil
		case e.Type == "system", e.Type == "rate_limit_event":
			// Hooks' events and the plan's meter come before the init in Claude Code 2.1.284; none is a turn.
			return nil
		}
		return fmt.Errorf("a %q event came before the CLI's init event, so its login (apiKeySource) is unknown: refused as possible API billing", e.Type)
	}
}

// ErrOverage is a session whose plan's meter (a rate_limit_event) shows overage in use: usage
// billed beyond the plan. The CLI is stopped, and the run keeps its .started marker.
var ErrOverage = errors.New("overage in use (usage billed beyond the plan)")

// overageInUse is the path of the first field in a rate_limit_event's info that may say overage
// is in use, "" when none does. Claude Code documents isUsingOverage only, so any field named for
// overage (or inside one) that is true, or a status that reads as in use, counts; a flag named
// for overage being disabled does not.
func overageInUse(v any, under bool, path string) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			key := strings.ToLower(k)
			if strings.Contains(key, "disabled") {
				continue
			}
			if p := overageInUse(x[k], under || strings.Contains(key, "overage"), path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		for i, y := range x {
			if p := overageInUse(y, under, fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
	case bool:
		if under && x {
			return path
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "in_use", "in use", "inuse", "using", "active":
			if under {
				return path
			}
		}
	}
	return ""
}

func (Claude) Parse(stdout []byte, b manifest.Backend) Outcome { return parseStreamJSON(stdout, b) }

// settingValue writes a setting that reads as JSON (true, 3, {"a": 1}) as that value, and
// anything else as a string.
func settingValue(v string) any {
	var x any
	if err := json.Unmarshal([]byte(v), &x); err == nil {
		return x
	}
	return v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func envList(maps ...map[string]string) []string {
	var out []string
	for _, m := range maps {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			out = append(out, k+"="+m[k])
		}
	}
	return out
}

// The stream-json events the parser reads; other events and fields are ignored.
type event struct {
	Type         string   `json:"type"`
	Subtype      string   `json:"subtype"`
	SessionID    string   `json:"session_id"`
	Message      *message `json:"message"`
	Result       string   `json:"result"`
	IsError      bool     `json:"is_error"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	Usage        *usage   `json:"usage"`
	// Error is a string on an api_retry event and on an assistant message that failed; read raw so
	// that another shape does not cost the event.
	Error json.RawMessage `json:"error"`
}

type message struct {
	Content []content `json:"content"`
	Usage   *usage    `json:"usage"`
}

type content struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type usage struct {
	Input         *int64 `json:"input_tokens"`
	CacheCreation *int64 `json:"cache_creation_input_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
	Output        *int64 `json:"output_tokens"`
}

// parseStreamJSON reads Claude Code's stream-json transcript: tool calls and the file reads with
// their result sizes from the messages, the answer, session, cumulative usage and cost from the
// result event, and the last turn's context from the last assistant message's usage.
func parseStreamJSON(stdout []byte, b manifest.Backend) Outcome {
	o := Outcome{Usage: record.Usage{Source: record.Unavailable}, Cost: record.Cost{Source: record.Unavailable}}
	tools := map[string]int{}
	readIDs := map[string]bool{}
	reads := 0
	var readBytes int64
	var last *usage
	var result *event
	// The last limit-shaped error the CLI retried, or an assistant message carried.
	var limitErr string
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	seen := false
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		seen = true
		if e.SessionID != "" {
			o.SessionID = e.SessionID
		}
		if c := limitCategory(e.Error); c != "" && (e.Type == "assistant" || e.Type == "system" && e.Subtype == "api_retry") {
			limitErr = "the CLI reported a " + c + " error"
		} else if e.Type == "assistant" {
			// A turn came through after the retries: a later failure is the task's, not the limit's.
			limitErr = ""
		}
		switch e.Type {
		case "assistant":
			if e.Message == nil {
				continue
			}
			if e.Message.Usage != nil {
				last = e.Message.Usage
			}
			for _, c := range e.Message.Content {
				if c.Type == "tool_use" {
					tools[c.Name]++
					if c.Name == "Read" {
						reads++
						readIDs[c.ID] = true
					}
				}
			}
		case "user":
			if e.Message == nil {
				continue
			}
			for _, c := range e.Message.Content {
				if c.Type == "tool_result" && readIDs[c.ToolUseID] {
					readBytes += int64(len(resultText(c.Content)))
				}
			}
		case "result":
			r := e
			result = &r
		}
	}
	o.Observed = record.Observed{Observable: seen, ToolCalls: tools, SourceReads: &reads, SourceBytes: &readBytes}
	if !seen {
		o.Observed = record.Observed{}
	}
	if result == nil {
		o.Err = "no result event in the transcript"
		o.Limit = limitErr
		return o
	}
	o.Answer = result.Result
	if result.IsError {
		o.Err = "the CLI reported an error: " + result.Subtype
		// A retried limit explains an empty result only: text that names another error is that error.
		if o.Limit = limitText(result.Result); o.Limit == "" && strings.TrimSpace(result.Result) == "" {
			o.Limit = limitErr
		}
	} else if a := strings.TrimSpace(result.Result); limitAnswer.MatchString(a) {
		o.Err, o.Limit = "the CLI gave its usage-limit message as the answer", a
	}
	if u := result.Usage; u != nil && u.Input != nil && u.CacheCreation != nil && u.CacheRead != nil && u.Output != nil {
		o.Usage = record.Usage{Source: record.Reported, Semantics: record.SemanticsFor(b.CLI),
			Input: u.Input, CacheCreation: u.CacheCreation, CacheRead: u.CacheRead, Output: u.Output}
		if last != nil && last.Input != nil && last.CacheCreation != nil && last.CacheRead != nil {
			ctx := *last.Input + *last.CacheCreation + *last.CacheRead
			o.Usage.LastTurnContext = &ctx
		}
	}
	if result.TotalCostUSD != nil {
		// Claude Code's figure is what the tokens would cost at API prices, also on a subscription.
		o.Cost = record.Cost{Source: record.Reported, Kind: "api-equivalent", USD: result.TotalCostUSD, Model: b.Model}
	}
	return o
}

// limitCategory is an error category of Claude Code's (the api_retry event's, an assistant
// message's) that says the plan or the service refused the request, not the task: "" otherwise.
func limitCategory(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	switch s {
	case "rate_limit", "overloaded", "billing_error":
		return s
	}
	return ""
}

// limitWords are the phrases of a usage limit, a rate limit or an overload in an error's text:
// Claude Code's own ("Claude AI usage limit reached|<reset>", "You've hit your session limit"),
// and the API's 429 and 529 as it relays them ("API Error: 429", "status 529"), never a bare number.
var limitWords = regexp.MustCompile(`(?i)\b(usage|session|weekly|rate)[ _-]?limit|\bhit your (\w+ )?limit|overloaded|\b(error|status|code)\W{1,3}(429|529)\b`)

// limitAnswer is Claude Code's usage-limit message as the whole of a result that is not marked as
// an error, which some versions give: only the whole answer, never a mention within one.
var limitAnswer = regexp.MustCompile(`^(?:Claude AI usage limit reached\|\d+|You['\x{2019}]ve hit your (?:\w+ )?limit\b.{0,80})$`)

// limitText is the first line of an error text that names a limit, trimmed; "" when it names none.
// Only error texts are read: a successful answer may well talk about rate limits.
func limitText(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); limitWords.MatchString(line) {
			if len(line) > 300 {
				line = line[:300] + "..."
			}
			return line
		}
	}
	return ""
}

// resultText is a tool result's text: a string, or the text blocks of a list.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type, Text string
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, x := range blocks {
			b.WriteString(x.Text)
		}
		return b.String()
	}
	return ""
}

// Fake stands in for a coding CLI in tests and synthetic manifests: Program (with Env) is started
// with the prompt on stdin and must print a stream-json transcript whose init event shows the
// subscription login, as FakeAgent does. It is never a model.
type Fake struct {
	Program []string
	Env     []string
}

func (f Fake) Command(c Cell) (Invocation, error) {
	if len(f.Program) == 0 {
		return Invocation{}, fmt.Errorf("the fake adapter has no program")
	}
	return Invocation{
		Argv: slices.Clone(f.Program), Dir: c.Workspace, Stdin: c.Task.Prompt,
		Env:     append(slices.Clone(f.Env), envList(c.Backend.Env, c.Condition.Env)...),
		Timeout: time.Duration(c.Execution.TimeoutSeconds) * time.Second,
		Watch:   subscriptionOnly(),
	}, nil
}

func (Fake) Parse(stdout []byte, b manifest.Backend) Outcome { return parseStreamJSON(stdout, b) }

// FakeAgent is the fake CLI: it answers with the prompt, reads one file, and prints a synthetic
// stream-json transcript with fixed usage. Attempts up to failUntil fail without a result.
func FakeAgent(stdin io.Reader, stdout io.Writer, attempt, failUntil int) int {
	prompt, _ := io.ReadAll(stdin)
	session := fmt.Sprintf("fake-session-%d", attempt)
	emit := func(v any) {
		data, _ := json.Marshal(v)
		stdout.Write(append(data, '\n'))
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": session, "apiKeySource": "none", "synthetic": true})
	if attempt <= failUntil {
		return 1
	}
	u := map[string]int{"input_tokens": 100, "cache_creation_input_tokens": 10, "cache_read_input_tokens": 1000, "output_tokens": 20}
	emit(map[string]any{"type": "assistant", "message": map[string]any{"usage": u,
		"content": []any{map[string]any{"type": "tool_use", "id": "fake-read-1", "name": "Read", "input": map[string]string{"file_path": "a.go"}}}}})
	emit(map[string]any{"type": "user", "message": map[string]any{
		"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "fake-read-1", "content": "synthetic file content"}}}})
	emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": string(prompt),
		"session_id": session, "total_cost_usd": 0, "usage": u})
	return 0
}
