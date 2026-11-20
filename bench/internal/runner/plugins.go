package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const pluginListTimeout = time.Minute

// InstalledPlugins lists Claude Code's installed plugins with `claude plugin list --json`, run in
// env: those enabled in the user's settings, synced to the account, or loaded for the session.
func InstalledPlugins(env []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "plugin", "list", "--json")
	cmd.Env = env
	cmd.WaitDelay = time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude plugin list --json: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return ParsePluginList(out.Bytes())
}

// ParsePluginList reads the ids from `claude plugin list --json`, a list of objects with an id each.
func ParsePluginList(data []byte) ([]string, error) {
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("reading the plugin list: %w", err)
	}
	ids := make([]string, 0, len(list))
	for i, p := range list {
		if p.ID == "" {
			return nil, fmt.Errorf("reading the plugin list: entry %d has no id", i)
		}
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// WorkerSetup drops CLAUDE_CODE_PLUGIN_DIRS from environ, since plugins loaded from a directory
// would come back in every session, and, when a claude backend runs, lists the installed plugins
// so each cell can turn them off. A list that cannot be read stops the execution.
func WorkerSetup(claude bool, environ []string, list func([]string) ([]string, error)) ([]string, []string, error) {
	var env []string
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, "CLAUDE_CODE_PLUGIN_DIRS") {
			env = append(env, kv)
		}
	}
	if !claude {
		return nil, env, nil
	}
	plugins, err := list(env)
	if err != nil {
		return nil, nil, errors.Join(errors.New("refusing to run: the installed plugins could not be listed, so they cannot be turned off in the workers"), err)
	}
	return plugins, env, nil
}
