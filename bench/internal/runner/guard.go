// Package runner executes a benchmark manifest's plan, one fresh workspace per run, through an
// adapter per coding CLI (claude first, and a fake one for tests and synthetic fixtures). Its real
// execution path refuses to start unless the approval matches the plan and no provider API key is
// set; DryRun prints every planned cell and runs nothing.
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// ApprovalFlag is the flag a real run needs, set to the plan's run count.
const ApprovalFlag = "--i-have-approval-for-N-runs"

// ProviderKeysSet lists, sorted, the variables in environ that would hand a provider API key or a
// pay-per-token route to a CLI and are set to something. Only names are returned, never values.
func ProviderKeysSet(environ []string) []string {
	var out []string
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && manifest.IsProviderKeyVar(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// Authorize is the gate in front of every real run. It refuses a manifest with any issue (an
// unpinned identity, a candidate task, a budget short of the plan included), an approval that is
// not exactly the plan's run count, an environment that sets a provider API key, and, for a
// claude backend, Claude Code user settings that would bill per token.
func Authorize(m *manifest.Manifest, approval int, environ []string) error {
	var errs []string
	for _, i := range m.Validate() {
		errs = append(errs, "manifest: "+i.String())
	}
	if n := m.PlannedRuns(); approval != n {
		errs = append(errs, fmt.Sprintf("a real run needs %s %d, the plan's run count, given by the owner's approval of that budget (got %d)",
			ApprovalFlag, n, approval))
	}
	if keys := ProviderKeysSet(environ); len(keys) > 0 {
		errs = append(errs, fmt.Sprintf("provider API key variables are set: %s; unset them, the benchmark runs CLIs on their subscription plans and never on API access",
			strings.Join(keys, ", ")))
	}
	if slices.ContainsFunc(m.Backends, func(b manifest.Backend) bool { return b.CLI == "claude" }) {
		if issues := ClaudeUserSettingsBilling(environ); len(issues) > 0 {
			errs = append(errs, fmt.Sprintf("Claude Code's user settings would bill per token: %s; remove them, the benchmark runs on the subscription login",
				strings.Join(issues, "; ")))
		}
	}
	if len(errs) > 0 {
		return errors.New("refusing to run:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// scrub is environ without any variable that would hand a CLI an API key or a pay-per-token
// route, as a second layer behind Authorize. The subscription's own login
// (CLAUDE_CODE_OAUTH_TOKEN) stays: a headless CLI needs it.
func scrub(environ []string) []string {
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if manifest.IsProviderKeyVar(k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// The user settings that hand Claude Code a key or a cloud credential of their own.
var billingSettings = []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport"}

// ClaudeUserSettingsBilling reads the user settings a claude run loads (settings.json and
// .claude.json in CLAUDE_CONFIG_DIR, or ~/.claude/settings.json and ~/.claude.json) and lists
// what would bill per token: a key helper or cloud credential command, an env block with a
// billing variable, a login forced away from claude.ai, or a Console login (primaryApiKey). A
// file that cannot be read as JSON is listed too. Only names are returned, never values.
func ClaudeUserSettingsBilling(environ []string) []string {
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[strings.ToUpper(k)] = v
		}
	}
	var settings, global []string
	if dir := env["CLAUDE_CONFIG_DIR"]; dir != "" {
		settings, global = []string{filepath.Join(dir, "settings.json")}, []string{filepath.Join(dir, ".claude.json")}
	} else {
		var homes []string
		for _, k := range []string{"HOME", "USERPROFILE"} {
			if h := env[k]; h != "" && !slices.Contains(homes, h) {
				homes = append(homes, h)
			}
		}
		if len(homes) == 0 {
			if h, err := os.UserHomeDir(); err == nil {
				homes = append(homes, h)
			}
		}
		for _, h := range homes {
			settings = append(settings, filepath.Join(h, ".claude", "settings.json"))
			global = append(global, filepath.Join(h, ".claude.json"))
		}
	}
	var out []string
	for _, p := range settings {
		var raw map[string]json.RawMessage
		if !readSettings(p, &raw, &out) {
			continue
		}
		for _, k := range billingSettings {
			if v, ok := raw[k]; ok && string(v) != "null" && string(v) != `""` {
				out = append(out, p+" sets "+k)
			}
		}
		var block map[string]json.RawMessage
		if json.Unmarshal(raw["env"], &block) == nil {
			var names []string
			for k := range block {
				if manifest.IsProviderKeyVar(k) {
					names = append(names, k)
				}
			}
			sort.Strings(names)
			for _, k := range names {
				out = append(out, p+" sets env "+k)
			}
		} else if raw["env"] != nil {
			out = append(out, p+" has an env that is not an object")
		}
		var login string
		if v, ok := raw["forceLoginMethod"]; ok && (json.Unmarshal(v, &login) != nil || login != "claudeai") {
			out = append(out, p+" sets forceLoginMethod to a login other than claudeai")
		}
	}
	for _, p := range global {
		var g map[string]json.RawMessage
		if readSettings(p, &g, &out) {
			if v, ok := g["primaryApiKey"]; ok && string(v) != "null" && string(v) != `""` {
				out = append(out, p+" holds a Console login (primaryApiKey)")
			}
		}
	}
	return out
}

// readSettings decodes the JSON file at p into v. A missing file is no settings; any other
// failure is listed in issues, without the file's content.
func readSettings(p string, v any, issues *[]string) bool {
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		*issues = append(*issues, p+" cannot be read")
		return false
	}
	if json.Unmarshal(data, v) != nil {
		*issues = append(*issues, p+" is not valid JSON")
		return false
	}
	return true
}
