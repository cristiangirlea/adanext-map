package runner

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// DryRun prints every planned cell and the command its first attempt would run, and what a real
// run would still refuse. It starts nothing: no checkout, no index, no CLI.
func DryRun(m *manifest.Manifest, w io.Writer, fake Fake, environ []string) {
	label := ""
	if m.Synthetic {
		label = " SYNTHETIC (fake backend, not measurements)"
	}
	if m.Scale == "smoke" {
		label += " SMOKE"
	}
	n := m.PlannedRuns()
	fmt.Fprintf(w, "adamap-bench dry run: manifest %s, scale %s%s, %d planned runs, budget %d\n", m.Name, m.Scale, label, n, m.Budget.MaxRuns)
	issues := m.Validate()
	if len(issues) == 0 {
		fmt.Fprintln(w, "manifest: valid")
	}
	for _, i := range issues {
		kind := "structural"
		if i.Blocker {
			kind = "blocks a run"
		}
		fmt.Fprintf(w, "manifest issue (%s): %s\n", kind, i)
	}
	if keys := ProviderKeysSet(environ); len(keys) > 0 {
		fmt.Fprintf(w, "a real run would refuse: provider API key variables are set: %s\n", strings.Join(keys, ", "))
	} else {
		fmt.Fprintln(w, "provider API key variables: none set")
	}
	fmt.Fprintf(w, "a real run needs %s %d, after the owner approves that budget\n\n", ApprovalFlag, n)

	tasks := map[string]manifest.Task{}
	for _, t := range m.Tasks {
		tasks[t.ID] = t
	}
	conds := map[string]manifest.Condition{}
	for _, c := range m.Conditions {
		conds[c.ID] = c
	}
	backends := map[string]manifest.Backend{}
	for _, b := range m.Backends {
		backends[b.ID] = b
	}
	o := Options{Fake: fake}
	for _, run := range m.Plan() {
		index := run.Index
		if index == "" {
			index = "-"
		}
		fmt.Fprintf(w, "run %d %s task=%s condition=%s backend=%s repeat=%d block=%d position=%d cache=%s index=%s\n",
			run.Seq, run.ID, run.Task, run.Condition, run.Backend, run.Repeat, run.Block, run.Position, run.Cache, index)
		t := tasks[run.Task]
		c := Cell{Run: run, Task: t, Condition: conds[run.Condition], Backend: backends[run.Backend], Execution: m.Execution,
			Workspace: filepath.Join("<work>", run.ID, "ws"), StateDir: filepath.Join("<work>", run.ID, "state"), Attempt: 1}
		fmt.Fprintf(w, "  workspace: fresh shallow checkout of %s at %s\n", t.Repository.Name, t.Revision)
		if c.Condition.Inject != nil {
			c.Injected = "<output of " + strings.Join(c.Condition.Inject, " ") + ">"
		}
		ad, err := o.adapter(c.Backend.CLI)
		if err != nil {
			fmt.Fprintf(w, "  command: none: %v\n", err)
			continue
		}
		inv, err := ad.Command(c)
		if err != nil {
			fmt.Fprintf(w, "  command: none: %v\n", err)
			continue
		}
		fmt.Fprintf(w, "  command: %s  (prompt on stdin, %d bytes; timeout %s)\n", quoteArgs(inv.Argv), len(inv.Stdin), inv.Timeout)
	}
}

func quoteArgs(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'") {
			out[i] = fmt.Sprintf("%q", a)
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}
