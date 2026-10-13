package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// executable is the path install writes as the server's command: the running binary's.
func executable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.Abs(exe); err != nil {
		t.Fatal(err)
	}
	return exe
}

// adamapEntry reads .mcp.json in repo and returns its adamap server entry.
func adamapEntry(t *testing.T, repo string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf(".mcp.json is not JSON: %v\n%s", err, data)
	}
	return doc.MCPServers["adamap"]
}

func checkEntry(t *testing.T, e map[string]any, repo string) {
	t.Helper()
	args, _ := e["args"].([]any)
	if e["command"] != executable(t) || len(args) != 3 || args[0] != "mcp" || args[1] != "--repo" || args[2] != repo {
		t.Errorf("adamap entry %v, want command %s and args mcp --repo %s", e, executable(t), repo)
	}
}

func TestInstallClaudeCreatesMCPJSON(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, "install", "--claude", "--repo", repo)
	if !strings.Contains(out, ".mcp.json") {
		t.Errorf("install should say what it wrote: %q", out)
	}
	e := adamapEntry(t, repo)
	checkEntry(t, e, repo)
	if e["type"] != "stdio" {
		t.Errorf("type %v, want stdio", e["type"])
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".mcp.json"))
	if !strings.HasPrefix(string(data), "{\n  \"mcpServers\": {\n    \"adamap\": {\n") || !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("unexpected layout:\n%s", data)
	}
}

func TestInstallClaudeTakesARelativeRepo(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	mustRun(t, "install", "--claude")
	abs, _ := filepath.Abs(".")
	checkEntry(t, adamapEntry(t, repo), abs)
}

const existingMCPJSON = `{
  "mcpServers": {
    "zeta": {
      "command": "npx",
      "args": ["-y", "server-zeta"],
      "env": {"B": "2", "A": "1"}
    },
    "adamap": {
      "command": "/old/adamap",
      "args": ["mcp"],
      "env": {"X": "y"}
    }
  },
  "other": true
}
`

func TestInstallClaudeUpdatesItsEntryAndKeepsTheRest(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".mcp.json")
	writeFile(t, path, existingMCPJSON)
	mustRun(t, "install", "--claude", "--repo", repo)

	e := adamapEntry(t, repo)
	checkEntry(t, e, repo)
	if env, _ := e["env"].(map[string]any); env["X"] != "y" {
		t.Errorf("the entry's other fields should stay: %v", e)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	zeta := doc["mcpServers"].(map[string]any)["zeta"].(map[string]any)
	if zeta["command"] != "npx" || doc["other"] != true {
		t.Errorf("other entries should stay: %s", s)
	}
	// Order stays: zeta before adamap, mcpServers before other, B before A.
	for _, pair := range [][2]string{{`"zeta"`, `"adamap"`}, {`"mcpServers"`, `"other"`}, {`"B"`, `"A"`}, {`"command": "npx"`, `"env"`}} {
		if i, j := strings.Index(s, pair[0]), strings.Index(s, pair[1]); i < 0 || j < 0 || i > j {
			t.Errorf("%s should come before %s:\n%s", pair[0], pair[1], s)
		}
	}

	// Installing again changes nothing.
	mustRun(t, "install", "--claude", "--repo", repo)
	if again, _ := os.ReadFile(path); string(again) != s {
		t.Errorf("a second install changed the file:\n%s\nthen\n%s", s, again)
	}
}

func TestInstallClaudeAddsItsEntryBesideOthers(t *testing.T) {
	for _, tc := range []struct{ name, existing string }{
		{"servers without adamap", `{"mcpServers": {"zeta": {"command": "z"}}}`},
		{"no servers yet", `{"other": 1}`},
		{"empty file", ""},
		{"byte order mark", "\ufeff{\"mcpServers\": {\"zeta\": {\"command\": \"z\"}}}"},
		{"duplicate adamap", `{"mcpServers": {"adamap": {"command": "a"}, "zeta": {"command": "z"}, "adamap": {"command": "b"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, ".mcp.json")
			writeFile(t, path, tc.existing)
			mustRun(t, "install", "--claude", "--repo", repo)
			checkEntry(t, adamapEntry(t, repo), repo)
			data, _ := os.ReadFile(path)
			if strings.Contains(tc.existing, "zeta") && !strings.Contains(string(data), `"zeta": {`) {
				t.Errorf("zeta was lost:\n%s", data)
			}
			if strings.Count(string(data), `"adamap"`) != 1 {
				t.Errorf("want one adamap entry:\n%s", data)
			}
		})
	}
}

func TestInstallClaudeRefusesAFileItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, existing, want string }{
		{"not JSON", `{"mcpServers": `, "JSON"},
		{"not an object", `["a"]`, "object"},
		{"servers not an object", `{"mcpServers": []}`, "mcpServers"},
		{"entry not an object", `{"mcpServers": {"adamap": "x"}}`, "adamap"},
		{"trailing data", `{} {}`, "after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, ".mcp.json")
			writeFile(t, path, tc.existing)
			code, out, errOut := runCmd("install", "--claude", "--repo", repo)
			if code != 1 || out != "" || !strings.Contains(errOut, tc.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 1 naming %q", code, out, errOut, tc.want)
			}
			if data, _ := os.ReadFile(path); string(data) != tc.existing {
				t.Errorf("the file was changed: %q", data)
			}
		})
	}
}

func TestInstallClaudeRefusesALink(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "elsewhere.json")
	writeFile(t, target, "{}")
	if err := os.Symlink(target, filepath.Join(repo, ".mcp.json")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	code, _, errOut := runCmd("install", "--claude", "--repo", repo)
	if code != 1 || !strings.Contains(errOut, "not a regular file") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if data, _ := os.ReadFile(target); string(data) != "{}" {
		t.Errorf("the link's target was written: %q", data)
	}
}

func TestInstallClaudeRefusesAFileOverTheLimit(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".mcp.json")
	big := strings.Repeat(" ", maxMCPJSONBytes+1)
	writeFile(t, path, big)
	code, out, errOut := runCmd("install", "--claude", "--repo", repo)
	if code != 1 || out != "" || !strings.Contains(errOut, "larger than") {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 1 saying the file is too large", code, out, errOut)
	}
	if data, _ := os.ReadFile(path); string(data) != big {
		t.Errorf("the file was changed: %d bytes", len(data))
	}
}

// noFiles fails the test if dir holds anything.
func noFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("%s should hold nothing, but holds %s", dir, e.Name())
	}
}

func TestInstallPrintIsTheDefault(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, "install", "--repo", repo)
	if printed := mustRun(t, "install", "--print", "--repo", repo); printed != out {
		t.Errorf("install and install --print differ:\n%s\n%s", out, printed)
	}
	exe, _ := json.Marshal(executable(t))
	for _, want := range []string{`"mcpServers": {`, `"adamap": {`, `"command": ` + string(exe), "[mcp_servers.adamap]", "Claude Code", "Codex", "~/.codex/config.toml"} {
		if !strings.Contains(out, want) {
			t.Errorf("--print should show %q:\n%s", want, out)
		}
	}
	noFiles(t, repo)
}

func TestInstallCodexPrintsTheTOMLAndWritesNothing(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out := mustRun(t, "install", "--codex", "--repo", repo)
	want := "[mcp_servers.adamap]\ncommand = " + tomlString(executable(t)) + "\nargs = [\"mcp\", \"--repo\", " + tomlString(repo) + "]\n"
	if out != want {
		t.Errorf("--codex:\n got %q\nwant %q", out, want)
	}
	noFiles(t, repo)
	noFiles(t, home)
}

func TestTOMLStringEscapes(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Program Files\adamap.exe`: `"C:\\Program Files\\adamap.exe"`,
		`say "hi"`:                    `"say \"hi\""`,
		"tab\tnew\nline\x01\x7f":      `"tab\tnew\nline\u0001\u007f"`,
		"é":                           `"é"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestInstallUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"install", "--claude", "--codex"},
		{"install", "--print", "--claude"},
		{"install", "extra"},
		{"install", "--nope"},
	} {
		code, out, errOut := runCmd(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: adamap install") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if code, _, _ := runCmd("install", "--claude", "--repo", filepath.Join(t.TempDir(), "missing")); code != 1 {
		t.Errorf("a --repo that is not a directory: exit %d, want 1", code)
	}
	if out := mustRun(t, "install", "-h"); !strings.Contains(out, "--claude") {
		t.Errorf("install -h: %q", out)
	}
	if out := mustRun(t, "help"); !strings.Contains(out, "\n  install ") {
		t.Errorf("help should list install: %q", out)
	}
}

func TestInstallClaudeReadsTheLastOfADuplicateKey(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".mcp.json"), `{
  "mcpServers": {"old": {"command": "o"}},
  "mcpServers": {"github": {"command": "g"}, "adamap": {"command": "a"}, "adamap": {"command": "b", "env": {"K": "v"}}}
}`)
	mustRun(t, "install", "--claude", "--repo", repo)
	data, _ := os.ReadFile(filepath.Join(repo, ".mcp.json"))
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MCPServers["github"] == nil || doc.MCPServers["old"] != nil {
		t.Errorf("the servers in effect are the last mcpServers' ones:\n%s", data)
	}
	if env, _ := doc.MCPServers["adamap"]["env"].(map[string]any); env["K"] != "v" {
		t.Errorf("the adamap entry in effect is the last one:\n%s", data)
	}
	checkEntry(t, doc.MCPServers["adamap"], repo)
}
