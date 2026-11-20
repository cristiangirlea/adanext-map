package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/runner"
)

const goplsLSP = "gopls-lsp@claude-plugins-official"

func TestClaudeCommandTurnsOffTheAccountsSetup(t *testing.T) {
	m := benchtest.Pilot()
	for i := range m.Conditions {
		if m.Conditions[i].ID == "lsp" {
			m.Conditions[i].Settings = map[string]string{"enabledPlugins": `{"` + goplsLSP + `": true}`}
		}
	}
	for cond, wantLSP := range map[string]bool{"none": false, "lsp": true} {
		c := cell(m, cond)
		c.Plugins = []string{"design@synced", goplsLSP}
		inv, err := runner.Claude{}.Command(c)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(inv.Argv, "--disable-slash-commands") {
			t.Fatalf("%s: argv %q", cond, inv.Argv)
		}
		var settings struct {
			OutputStyle    string          `json:"outputStyle"`
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if err := json.Unmarshal([]byte(inv.Files[flag(inv.Argv, "--settings")]), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.OutputStyle != "default" {
			t.Fatalf("%s: output style %q", cond, settings.OutputStyle)
		}
		on, listed := settings.EnabledPlugins["design@synced"]
		if on || !listed {
			t.Fatalf("%s: an installed plugin is not turned off: %v", cond, settings.EnabledPlugins)
		}
		if settings.EnabledPlugins[goplsLSP] != wantLSP {
			t.Fatalf("%s: %s = %v, want %v", cond, goplsLSP, settings.EnabledPlugins[goplsLSP], wantLSP)
		}
	}
}

func TestParsePluginList(t *testing.T) {
	got, err := runner.ParsePluginList([]byte(`[{"id": "a@x", "enabled": true}, {"id": "b@synced", "scope": "synced"}]`))
	if err != nil || !slices.Equal(got, []string{"a@x", "b@synced"}) {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := runner.ParsePluginList([]byte(`[]`)); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %q, %v", got, err)
	}
	for _, bad := range []string{``, `{"id": "a"}`, `[{"name": "a"}]`, `[{"id": ""}]`} {
		if _, err := runner.ParsePluginList([]byte(bad)); err == nil {
			t.Fatalf("%q: no error", bad)
		}
	}
}

func TestExecuteRefusesWhenThePluginsCannotBeListed(t *testing.T) {
	m := benchtest.Pilot()
	home := t.TempDir()
	o := runner.Options{
		Manifest: m, Approval: m.PlannedRuns(),
		Environ:    append(cleanEnviron(), "CLAUDE_CONFIG_DIR="+home),
		RecordsDir: filepath.Join(t.TempDir(), "records"), WorkDir: filepath.Join(t.TempDir(), "work"),
		ListPlugins: func([]string) ([]string, error) { return nil, errors.New("no claude here") },
	}
	if err := runner.Authorize(m, m.PlannedRuns(), o.Environ); err != nil {
		t.Fatalf("the pilot fixture does not pass Authorize: %v", err)
	}
	err := runner.Execute(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "plugins") || !strings.Contains(err.Error(), "no claude here") {
		t.Fatalf("err %v", err)
	}
	for _, dir := range []string{o.WorkDir, o.RecordsDir} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("%s was made", dir)
		}
	}
}

func TestExecutePassesTheListedPluginsAndDropsPluginDirs(t *testing.T) {
	var seen []string
	list := func(env []string) ([]string, error) {
		seen = env
		return []string{"a@x"}, nil
	}
	got, env, err := runner.WorkerSetup(true, []string{"PATH=/bin", "CLAUDE_CODE_PLUGIN_DIRS=/mods", "claude_code_plugin_dirs=/x"}, list)
	if err != nil || !slices.Equal(got, []string{"a@x"}) {
		t.Fatalf("plugins %q, %v", got, err)
	}
	for _, e := range [][]string{env, seen} {
		if !slices.Equal(e, []string{"PATH=/bin"}) {
			t.Fatalf("env %q", e)
		}
	}
	got, _, err = runner.WorkerSetup(false, nil, func([]string) ([]string, error) { t.Fatal("listed without a claude backend"); return nil, nil })
	if err != nil || got != nil {
		t.Fatalf("no claude backend: %q, %v", got, err)
	}
}
