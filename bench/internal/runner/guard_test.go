package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/runner"
)

const userSettingsIssue = "Claude Code's user settings"

// A claude run is refused when Claude Code's user settings would bill per token: an apiKeyHelper,
// an env block with a billing variable, a Console login, a login forced away from claude.ai, or a
// file that cannot be read. Only names are printed, never values.
func TestAuthorizeRefusesUserSettingsThatBill(t *testing.T) {
	m := benchtest.Pilot()
	cases := []struct {
		name, file, text, want string
	}{
		{"an api key helper", "settings.json", `{"apiKeyHelper": "/bin/secret-helper"}`, "apiKeyHelper"},
		{"an env key", "settings.json", `{"env": {"ANTHROPIC_API_KEY": "sk-secret"}}`, "ANTHROPIC_API_KEY"},
		{"an env gateway", "settings.json", `{"env": {"ANTHROPIC_BASE_URL": "https://secret.example"}}`, "ANTHROPIC_BASE_URL"},
		{"an env cloud switch", "settings.json", `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`, "CLAUDE_CODE_USE_BEDROCK"},
		{"a console login", ".claude.json", `{"primaryApiKey": "sk-secret"}`, "primaryApiKey"},
		{"a forced console login", "settings.json", `{"forceLoginMethod": "console"}`, "forceLoginMethod"},
		{"an unreadable file", "settings.json", `{"apiKeyHelper": "sk-secret"`, "settings.json"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.file), []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		err := runner.Authorize(m, m.PlannedRuns(), []string{"CLAUDE_CONFIG_DIR=" + dir, "HOME=" + t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), userSettingsIssue) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err %v", c.name, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("%s: the error prints a value: %v", c.name, err)
		}
	}
}

// Without CLAUDE_CONFIG_DIR the files are the home directory's ~/.claude/settings.json and
// ~/.claude.json; settings that do not bill pass.
func TestAuthorizeReadsTheHomeDirectorysClaudeSettings(t *testing.T) {
	m := benchtest.Pilot()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	write(t, settings, `{"env": {"BASH_DEFAULT_TIMEOUT_MS": "1000"}, "forceLoginMethod": "claudeai"}`)
	write(t, filepath.Join(home, ".claude.json"), `{"oauthAccount": {"emailAddress": "a@example.com"}}`)
	for _, environ := range [][]string{{"HOME=" + home}, {"USERPROFILE=" + home}} {
		if err := runner.Authorize(m, m.PlannedRuns(), environ); err != nil && strings.Contains(err.Error(), userSettingsIssue) {
			t.Fatalf("%v: settings that do not bill are refused: %v", environ, err)
		}
	}
	write(t, settings, `{"apiKeyHelper": "x"}`)
	for _, environ := range [][]string{{"HOME=" + home}, {"USERPROFILE=" + home}} {
		if err := runner.Authorize(m, m.PlannedRuns(), environ); err == nil || !strings.Contains(err.Error(), "apiKeyHelper") {
			t.Fatalf("%v: err %v", environ, err)
		}
	}
}

// A plan with no claude backend does not read Claude Code's settings.
func TestAuthorizeReadsClaudeSettingsOnlyForAClaudeBackend(t *testing.T) {
	m := benchtest.Synthetic()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "settings.json"), `{"apiKeyHelper": "x"}`)
	if err := runner.Authorize(m, m.PlannedRuns(), []string{"CLAUDE_CONFIG_DIR=" + dir}); err != nil && strings.Contains(err.Error(), userSettingsIssue) {
		t.Fatalf("err %v", err)
	}
}
