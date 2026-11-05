package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	if runtime.GOOS != "windows" {
		c.Probe = c.Probe[2:]
		c.Index.Command = c.Index.Command[2:]
		c.MCP.Command, c.MCP.Args = "codegraph", c.MCP.Args[2:]
	}
	probe, args := slices.Clone(c.Probe), slices.Clone(c.MCP.Args)
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
	if !slices.Equal(c.Probe, probe) || !slices.Equal(c.MCP.Args, args) {
		t.Fatal("manifest mutated")
	}
	if runtime.GOOS == "windows" {
		for _, command := range []string{got.Probe[0], got.Index.Command[0], got.MCP.Command} {
			if !filepath.IsAbs(command) {
				t.Fatalf("shell command is not absolute: %q", command)
			}
			resolved, err := filepath.EvalSymlinks(command)
			if err != nil {
				t.Fatal(err)
			}
			resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(resolvedWorkspace, resolved)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("shell command is inside workspace: %q", command)
			}
		}
		res := runProcess(context.Background(), got.Probe, workspace, os.Environ(), "", 5*time.Second)
		if res.err != nil || strings.TrimSpace(string(res.stdout)) != "fixture" {
			t.Fatalf("resolved shim: stdout %q stderr %q err %v", res.stdout, res.stderr, res.err)
		}
	}
	t.Setenv("PATH", workspace)
	if _, err := resolveCodegraph(c, workspace); err == nil {
		t.Fatal("accepted workspace executable")
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(filepath.Join(workspace, name)); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{workspace, root} {
			if err := os.WriteFile(filepath.Join(dir, "cmd.exe"), []byte("untrusted"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", workspace+string(os.PathListSeparator)+trusted)
			if _, err := resolveCodegraph(c, workspace); err == nil || !strings.Contains(err.Error(), "cmd") {
				t.Fatalf("accepted untrusted shell in %s: %v", dir, err)
			}
		}
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
