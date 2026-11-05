package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

func TestApprovalExplainsSessionsAndScope(t *testing.T) {
	m := benchtest.Synthetic()
	m.Execution.MaxAttempts = 2
	err := Authorize(m, 0, nil)
	var out bytes.Buffer
	DryRun(m, &out, Fake{}, nil)
	for name, text := range map[string]string{"approval": err.Error(), "dry run": out.String()} {
		for _, want := range []string{"maximum", "sessions", "records directory", "elsewhere", "limit restarts"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q: %s", name, want, text)
			}
		}
	}
}

func followupGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	argv := append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)
	out, err := exec.Command("git", argv...).CombinedOutput()
	if err != nil {
		t.Fatalf("git: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func followupRepo(t *testing.T) (string, string, string, Cell) {
	t.Helper()
	src := t.TempDir()
	followupGit(t, src, "init", "-q")
	os.WriteFile(filepath.Join(src, "a.go"), []byte("package a\n"), 0600)
	os.WriteFile(filepath.Join(src, "old_test.go"), []byte("package a\n// original\n"), 0600)
	followupGit(t, src, "add", ".")
	followupGit(t, src, "commit", "-qm", "base")
	pre := followupGit(t, src, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(src, "fix_test.go"), []byte("package a\n// fix\n"), 0600)
	followupGit(t, src, "add", ".")
	followupGit(t, src, "commit", "-qm", "fix")
	fix := followupGit(t, src, "rev-parse", "HEAD")
	ws := filepath.Join(t.TempDir(), "ws")
	if err := Checkout(src, pre, ws); err != nil {
		t.Fatal(err)
	}
	return src, pre, fix, Cell{Workspace: ws, Task: manifest.Task{Revision: pre, Provenance: manifest.Provenance{FixRevision: fix}}}
}

func TestFixTestsRejectWorkerAddedTests(t *testing.T) {
	for _, dir := range []string{".", "cache"} {
		t.Run(dir, func(t *testing.T) {
			src, _, fix, c := followupRepo(t)
			c.Condition.Index = &manifest.Index{Output: "cache"}
			target := filepath.Join(c.Workspace, dir, "escape_test.go")
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("package a\nimport (\"os\"; \"testing\")\nfunc TestMain(m *testing.M) { os.Exit(0) }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			_, undo, err := (&Options{}).overlay(c, src, fix, nil)
			if undo != nil {
				defer undo()
			}
			if err == nil || !strings.Contains(err.Error(), "worker-added") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestFixTestsRestoreExpectedTestsAndUndo(t *testing.T) {
	src, _, fix, c := followupRepo(t)
	p := filepath.Join(c.Workspace, "old_test.go")
	worker := "package a\n// worker replaced the existing tests\n"
	os.WriteFile(p, []byte(worker), 0600)
	_, undo, err := (&Options{}).overlay(c, src, fix, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	data, _ := os.ReadFile(p)
	if string(data) != "package a\n// original\n" {
		t.Errorf("untrusted test: %s", data)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(p)
	if string(data) != worker {
		t.Errorf("not restored: %s", data)
	}
}

func TestCheckRefusesDriftBeforeNextCommand(t *testing.T) {
	_, _, _, c := followupRepo(t)
	fp, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(c.Workspace, "leftover_test.go"), []byte("package a\n"), 0600)
	self, _ := os.Executable()
	_, err = (&Options{}).check(context.Background(), c, manifest.Check{ID: "next", Type: manifest.CheckCommand, Command: []string{self, "-test.run=^$"}}, &record.Record{After: fp}, "", os.Environ(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "tree") {
		t.Fatalf("error = %v", err)
	}
}

func TestWarmReuseRefusesAnotherRevision(t *testing.T) {
	o := &Options{WorkDir: t.TempDir()}
	c := Cell{Workspace: t.TempDir(), Task: manifest.Task{ID: "task", Revision: benchtest.Rev(1)}, Condition: manifest.Condition{ID: "cond", Index: &manifest.Index{Output: ".idx"}}, Backend: manifest.Backend{ID: "backend"}}
	cache := filepath.Join(o.WorkDir, "cache", "task.cond.backend")
	os.MkdirAll(filepath.Join(cache, "index"), 0700)
	os.WriteFile(filepath.Join(cache, "index", "data"), []byte("stale"), 0600)
	os.WriteFile(filepath.Join(cache, "meta.json"), []byte(`{"commit":"old","content":"old"}`), 0600)
	c.Run.Index = "reuse"
	_, err := o.index(context.Background(), c, record.Fingerprint{Commit: c.Task.Revision, Content: record.CleanContent}, os.Environ(), time.Second)
	if err == nil {
		t.Fatal("reused another revision's index")
	}
}

func TestGitIgnoresCallerRepository(t *testing.T) {
	_, pre, _, c := followupRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "nonexistent"))
	got, err := git(c.Workspace, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(got) != pre {
		t.Fatalf("head %q, error %v", got, err)
	}
}

func TestGitDisablesWorkerFilter(t *testing.T) {
	_, _, _, c := followupRepo(t)
	followupGit(t, c.Workspace, "config", "filter.planted.clean", "touch FILTER-RAN")
	followupGit(t, c.Workspace, "config", "filter.planted.required", "true")
	os.WriteFile(filepath.Join(c.Workspace, ".gitattributes"), []byte("*.go filter=planted\n"), 0600)
	os.WriteFile(filepath.Join(c.Workspace, "a.go"), []byte("package a\n// changed\n"), 0600)
	if _, err := Fingerprint(c.Workspace, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Workspace, "FILTER-RAN")); !os.IsNotExist(err) {
		t.Fatalf("filter executed: %v", err)
	}
}

func TestGitTimeoutIsBounded(t *testing.T) {
	oldTimeout, oldCommand := gitTimeout, gitCommand
	defer func() { gitTimeout, gitCommand = oldTimeout, oldCommand }()
	gitTimeout = 100 * time.Millisecond
	t.Setenv("BENCH_TEST_HELPER", "sleep")
	self, _ := os.Executable()
	gitCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, self, "-test.run=^$")
	}
	start := time.Now()
	_, err := git(t.TempDir(), "status")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("git exceeded timeout bound")
	}
}

func TestFixCheckNamesOverlaidTree(t *testing.T) {
	src, _, fix, c := followupRepo(t)
	before, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{}
	_, undo, err := o.overlay(c, src, fix, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	res, err := o.check(context.Background(), c, manifest.Check{ID: "fix", Type: manifest.CheckFixTests, Command: []string{self, "-test.run=^$"}}, &record.Record{After: before}, src, os.Environ(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(res)
	var got struct {
		CheckedTree record.Fingerprint `json:"checked_tree"`
	}
	json.Unmarshal(data, &got)
	if got.CheckedTree != expected {
		t.Fatalf("checked tree = %+v, want %+v", got.CheckedTree, expected)
	}
}

func TestFingerprintStillSeesSubmoduleCommitChanges(t *testing.T) {
	src, pre, fix, c := followupRepo(t)
	nested := filepath.Join(c.Workspace, "nested")
	if err := Checkout(src, pre, nested); err != nil {
		t.Fatal(err)
	}
	followupGit(t, c.Workspace, "add", "nested")
	followupGit(t, c.Workspace, "commit", "-qm", "gitlink")
	before, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fetch(nested, src, fix); err != nil {
		t.Fatal(err)
	}
	followupGit(t, nested, "checkout", "-q", "--detach", fix)
	after, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("submodule commit change disappeared from fingerprint")
	}
}
