package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

func TestLimitHitsUsesLastExactPrefix(t *testing.T) {
	for _, note := range []string{
		"limited: CLI said limit_hits: 91\nlimit_hits: 2\n",
		"limit_hits: 91\nlimit_hits: 2\n",
		"limit_hits: 2\n  limit_hits: 91\n",
	} {
		if got := limitHits([]byte(note)); got != 2 {
			t.Errorf("%q: got %d, want 2", note, got)
		}
	}
}

func TestCodegraphResolutionOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	trusted := filepath.Join(root, "trusted tools")
	for _, dir := range []string{workspace, trusted} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	name := "codegraph"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	for _, dir := range []string{workspace, trusted} {
		text := "@echo fixture\r\n"
		if dir == workspace {
			text = "@echo untrusted\r\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	t.Setenv("PATH", trusted+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := manifest.Condition{Kind: manifest.KindCodeGraph,
		Probe:  []string{"cmd", "/c", "codegraph", "--version"},
		Inject: []string{"codegraph", "map"},
		Index:  &manifest.Index{Command: []string{"cmd", "/c", "codegraph", "init", "-y", "."}},
		MCP:    &manifest.Server{Command: "cmd", Args: []string{"/c", "codegraph", "serve", "--mcp"}},
	}
	got, err := resolveCodegraph(c, workspace)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(trusted, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{got.Probe, got.Inject, got.Index.Command, append([]string{got.MCP.Command}, got.MCP.Args...)} {
		if !strings.Contains(strings.Join(argv, " "), want) {
			t.Errorf("unresolved command %q, want %s", argv, want)
		}
	}
	if c.Probe[2] != "codegraph" || c.MCP.Args[1] != "codegraph" {
		t.Fatal("manifest mutated")
	}
	if runtime.GOOS == "windows" {
		res := runProcess(context.Background(), got.Probe, workspace, os.Environ(), "", 5*time.Second)
		if res.err != nil || strings.TrimSpace(string(res.stdout)) != "fixture" {
			t.Fatalf("resolved shim: stdout %q stderr %q err %v", res.stdout, res.stderr, res.err)
		}
	}
	t.Setenv("PATH", workspace)
	if _, err := resolveCodegraph(c, workspace); err == nil {
		t.Fatal("accepted workspace executable")
	}
	t.Chdir(workspace)
	if _, err := resolveCodegraph(c, workspace); err == nil {
		t.Fatal("resolved from inside workspace")
	}
}

func TestCodegraphExplicitCommandsNeedNoPathLookup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := manifest.Condition{Kind: manifest.KindCodeGraph, Probe: []string{self}}
	if _, err := resolveCodegraph(c, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
