package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		ListPlugins: func(context.Context, []string) ([]string, error) { return nil, errors.New("no claude here") },
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

// claudeStub stands in for the claude CLI when the test binary runs under that name: it saves
// the settings file it was given and its environment to BENCH_TEST_DUMP, then answers as a
// subscription session that did nothing.
func claudeStub() int {
	io.Copy(io.Discard, os.Stdin)
	args := os.Args[1:]
	i := slices.Index(args, "--settings")
	if i < 0 || i+1 >= len(args) {
		return 2
	}
	settings, err := os.ReadFile(args[i+1])
	if err != nil {
		return 2
	}
	dump, _ := json.Marshal(map[string]any{"settings": json.RawMessage(settings), "args": args, "env": os.Environ()})
	f, err := os.CreateTemp(os.Getenv("BENCH_TEST_DUMP"), "cell-*.json")
	if err != nil {
		return 2
	}
	f.Write(dump)
	f.Close()
	fmt.Println(`{"type":"system","subtype":"init","apiKeySource":"none","session_id":"s"}`)
	fmt.Println(`{"type":"result","subtype":"success","is_error":false,"result":"pkg/a.go pkg/b.go","session_id":"s","usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}`)
	return 0
}

// Execute hands the listed plugins and the environment without CLAUDE_CODE_PLUGIN_DIRS to the
// claude cells it starts.
func TestExecuteGivesTheClaudeCellsTheListedPlugins(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	m.Synthetic = false
	m.Backends = benchtest.Pilot().Backends
	bin, dump := t.TempDir(), t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(bin, "claude"+filepath.Ext(self))
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	o := options(t, m, src, m.PlannedRuns())
	o.Environ = append(cleanEnviron(), "CLAUDE_CONFIG_DIR="+t.TempDir(), "CLAUDE_CODE_PLUGIN_DIRS=/mods", "BENCH_TEST_DUMP="+dump)
	var listedWith []string
	o.ListPlugins = func(_ context.Context, env []string) ([]string, error) {
		listedWith = env
		return []string{"a@x"}, nil
	}
	if err := runner.Authorize(m, m.PlannedRuns(), o.Environ); err != nil {
		t.Fatalf("the manifest does not pass Authorize: %v", err)
	}
	runner.Execute(context.Background(), o) // the cells are what this test reads, not the records
	cells, _ := filepath.Glob(filepath.Join(dump, "cell-*.json"))
	if len(cells) == 0 {
		t.Fatalf("no claude cell started; log:\n%s", o.Log)
	}
	if slices.ContainsFunc(listedWith, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CODE_PLUGIN_DIRS=") }) {
		t.Fatal("the plugins were listed with CLAUDE_CODE_PLUGIN_DIRS set")
	}
	for _, p := range cells {
		var cell struct {
			Settings struct {
				OutputStyle    string          `json:"outputStyle"`
				EnabledPlugins map[string]bool `json:"enabledPlugins"`
			}
			Args, Env []string
		}
		raw, _ := os.ReadFile(p)
		if err := json.Unmarshal(raw, &cell); err != nil {
			t.Fatal(err)
		}
		on, listed := cell.Settings.EnabledPlugins["a@x"]
		if on || !listed || cell.Settings.OutputStyle != "default" || !slices.Contains(cell.Args, "--disable-slash-commands") {
			t.Fatalf("cell %s: settings %+v args %q", p, cell.Settings, cell.Args)
		}
		if slices.ContainsFunc(cell.Env, func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), "CLAUDE_CODE_PLUGIN_DIRS=") }) {
			t.Fatalf("cell %s: CLAUDE_CODE_PLUGIN_DIRS reached the worker", p)
		}
	}
}

func TestWorkerSetupListsThePluginsAndDropsPluginDirs(t *testing.T) {
	var seen []string
	list := func(_ context.Context, env []string) ([]string, error) {
		seen = env
		return []string{"a@x"}, nil
	}
	got, env, err := runner.WorkerSetup(context.Background(), true, []string{"PATH=/bin", "CLAUDE_CODE_PLUGIN_DIRS=/mods", "claude_code_plugin_dirs=/x"}, list)
	if err != nil || !slices.Equal(got, []string{"a@x"}) {
		t.Fatalf("plugins %q, %v", got, err)
	}
	for _, e := range [][]string{env, seen} {
		if !slices.Equal(e, []string{"PATH=/bin"}) {
			t.Fatalf("env %q", e)
		}
	}
	got, _, err = runner.WorkerSetup(context.Background(), false, nil, func(context.Context, []string) ([]string, error) {
		t.Fatal("listed without a claude backend")
		return nil, nil
	})
	if err != nil || got != nil {
		t.Fatalf("no claude backend: %q, %v", got, err)
	}
}
