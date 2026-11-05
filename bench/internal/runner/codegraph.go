package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// Resolve before entering the checkout so cmd cannot select a task's shim.
func resolveCodegraph(c manifest.Condition, workspace string) (manifest.Condition, error) {
	if c.Kind != manifest.KindCodeGraph {
		return c, nil
	}
	position := func(argv []string) int {
		if len(argv) >= 3 && strings.EqualFold(argv[0], "cmd") && strings.EqualFold(argv[1], "/c") && argv[2] == "codegraph" {
			return 2
		}
		if len(argv) > 0 && argv[0] == "codegraph" {
			return 0
		}
		return -1
	}
	commands := [][]string{c.Probe, c.Inject}
	if c.Index != nil {
		commands = append(commands, c.Index.Command)
	}
	if c.MCP != nil {
		commands = append(commands, append([]string{c.MCP.Command}, c.MCP.Args...))
	}
	if !slices.ContainsFunc(commands, func(argv []string) bool { return position(argv) >= 0 }) {
		return c, nil
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return c, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return c, err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return c, err
	}
	inside := func(path string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if inside(cwd) {
		return c, fmt.Errorf("resolve codegraph from outside the workspace")
	}
	resolve := func(name string) (string, error) {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", name, err)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		if inside(path) {
			return "", fmt.Errorf("refusing %s inside the workspace: %s", name, path)
		}
		return path, nil
	}
	path, err := resolve("codegraph")
	if err != nil {
		return c, err
	}
	var shell string
	if slices.ContainsFunc(commands, func(argv []string) bool { return position(argv) == 2 }) {
		shell, err = resolve("cmd")
		if err != nil {
			return c, err
		}
	}
	replace := func(argv []string) []string {
		argv = slices.Clone(argv)
		if position(argv) == 2 {
			argv[0] = shell
			argv[2] = path
			// Disable cmd's startup scripts as well as its executable search.
			argv = slices.Insert(argv, 1, "/d")
		} else if position(argv) == 0 {
			argv[0] = path
		}
		return argv
	}
	c.Probe = replace(c.Probe)
	c.Inject = replace(c.Inject)
	if c.Index != nil {
		x := *c.Index
		x.Command = replace(x.Command)
		c.Index = &x
	}
	if c.MCP != nil {
		s := *c.MCP
		argv := replace(append([]string{s.Command}, s.Args...))
		s.Command, s.Args = argv[0], argv[1:]
		c.MCP = &s
	}
	return c, nil
}
