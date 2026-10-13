package main

import (
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// The example manifest and its candidate tasks are complete except for what the owner decides:
// every issue blocks a run (an unpinned identity, a candidate task), none is structural.
func TestTheExampleManifestHasOnlyOwnerBlockers(t *testing.T) {
	m, err := manifest.Load("../../manifest.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) < 10 || m.PlannedRuns() != m.Budget.MaxRuns {
		t.Fatalf("%d tasks, %d planned runs, budget %d", len(m.Tasks), m.PlannedRuns(), m.Budget.MaxRuns)
	}
	unpinned, candidates := 0, 0
	for _, i := range m.Validate() {
		switch {
		case !i.Blocker:
			t.Errorf("structural issue: %v", i)
		case strings.Contains(i.Msg, "not pinned"):
			unpinned++
		case strings.Contains(i.Msg, "candidate"):
			candidates++
		default:
			t.Errorf("unexpected blocker: %v", i)
		}
	}
	if candidates != len(m.Tasks) || unpinned == 0 {
		t.Fatalf("%d candidates of %d tasks, %d unpinned identities", candidates, len(m.Tasks), unpinned)
	}
	kinds := map[string]int{}
	repos := map[string]int{}
	for _, task := range m.Tasks {
		kinds[task.Kind]++
		repos[task.Repository.Name]++
		if task.Repository.Visibility == "private" && task.Provenance.Source != "pr" {
			t.Errorf("%s: a private repository's task comes from its PR title and revisions only", task.ID)
		}
	}
	for _, k := range manifest.TaskKinds {
		if kinds[k] == 0 {
			t.Errorf("no %s task", k)
		}
	}
	if repos["cristiangirlea/adanext-map"] == 0 || repos["cristiangirlea/harness"] == 0 {
		t.Errorf("tasks per repository %v", repos)
	}
	code, out, _ := call(nil, "run", "--manifest", "../../manifest.example.json", "--dry-run")
	if code != 0 || !strings.Contains(out, "240 planned runs") {
		t.Fatalf("dry run: exit %d, %.300q", code, out)
	}
}
