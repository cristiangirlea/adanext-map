package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
			before, err := Fingerprint(c.Workspace, nil)
			if err != nil {
				t.Fatal(err)
			}
			files, undo, err := (&Options{}).overlay(c, src, fix, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer undo()
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("added test still present: %v", err)
			}
			if !strings.Contains(strings.Join(files, ","), "escape_test.go") {
				t.Fatal("set-aside file not named")
			}
			if err := undo(); err != nil {
				t.Fatal(err)
			}
			restored, err := Fingerprint(c.Workspace, nil)
			if err != nil || restored != before {
				t.Fatalf("not restored: %+v, %v", restored, err)
			}
			self, _ := os.Executable()
			env := append(os.Environ(), "BENCH_TEST_ABSENT="+target)
			c.Condition.Index = nil
			ch := manifest.Check{ID: "fix", Type: manifest.CheckFixTests, Command: []string{self, "-test.run=^TestFollowupCheckHelper$"}}
			res, err := (&Options{}).check(context.Background(), c, ch, &record.Record{After: before}, src, env, 5*time.Second)
			if err != nil || !res.Passed || !strings.Contains(res.Output, "escape_test.go") {
				t.Fatalf("result %+v, error %v", res, err)
			}
			restored, err = Fingerprint(c.Workspace, nil)
			if err != nil || restored != before {
				t.Fatalf("check did not restore worker: %+v, %v", restored, err)
			}
		})
	}
}

func TestFixTestsSetAsideWorkerAddedSymlink(t *testing.T) {
	src, _, _, c := followupRepo(t)
	link := filepath.Join(c.Workspace, "escape_test.go")
	if err := os.Symlink("a.go", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	before, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "BENCH_TEST_ABSENT="+link)
	ch := manifest.Check{ID: "fix", Type: manifest.CheckFixTests, Command: []string{self, "-test.run=^TestFollowupCheckHelper$"}}
	res, err := (&Options{}).check(context.Background(), c, ch, &record.Record{After: before}, src, env, 5*time.Second)
	if err != nil || !res.Passed || !strings.Contains(res.Output, "set aside escape_test.go") {
		t.Fatalf("result %+v, error %v", res, err)
	}
	if target, err := os.Readlink(link); err != nil || target != "a.go" {
		t.Fatalf("restored link target %q, error %v", target, err)
	}
	restored, err := Fingerprint(c.Workspace, nil)
	if err != nil || restored != before {
		t.Fatalf("check did not restore worker: %+v, %v", restored, err)
	}
}

func TestFixTestsRecordWorkerAddedNonRegularTest(t *testing.T) {
	src, _, _, c := followupRepo(t)
	dir := filepath.Join(c.Workspace, "escape_test.go")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch := manifest.Check{ID: "fix", Type: manifest.CheckFixTests}
	res, err := (&Options{}).check(context.Background(), c, ch, &record.Record{After: before}, src, os.Environ(), time.Second)
	if err != nil || res.Passed || res.Exit != nil || !strings.Contains(res.Output, "escape_test.go") || res.OutputSHA256 != record.SHA256(res.Output) {
		t.Fatalf("result %+v, error %v", res, err)
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("worker directory not preserved: %v", err)
	}
	restored, err := Fingerprint(c.Workspace, nil)
	if err != nil || restored != before {
		t.Fatalf("failed check did not restore worker: %+v, %v", restored, err)
	}
}

func TestFixTestsRecordWorkerObstructedExpectedTest(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "linked-parent"} {
		t.Run(kind, func(t *testing.T) {
			src := t.TempDir()
			followupGit(t, src, "init", "-q")
			old := "old_test.go"
			if kind == "linked-parent" {
				old = "tests/old_test.go"
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(src, old)), 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string]string{"a.go": "package a\n", old: "package a\n// original\n"} {
				if err := os.WriteFile(filepath.Join(src, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			followupGit(t, src, "add", ".")
			followupGit(t, src, "commit", "-qm", "base")
			pre := followupGit(t, src, "rev-parse", "HEAD")
			// Sort this overlay first so rejection must undo an earlier replacement.
			if err := os.WriteFile(filepath.Join(src, "a.go"), []byte("package a\n// fix\n"), 0600); err != nil {
				t.Fatal(err)
			}
			followupGit(t, src, "add", ".")
			followupGit(t, src, "commit", "-qm", "fix")
			fix := followupGit(t, src, "rev-parse", "HEAD")
			ws := filepath.Join(t.TempDir(), "ws")
			if err := Checkout(src, pre, ws); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(ws, filepath.FromSlash(old))
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			why := "something other than a file"
			switch kind {
			case "symlink":
				if err := os.Symlink("a.go", target); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "linked-parent":
				if err := os.Remove(filepath.Dir(target)); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "old_test.go"), []byte("outside\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Dir(target)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				why = "link in the way"
				t.Cleanup(func() {
					data, err := os.ReadFile(filepath.Join(outside, "old_test.go"))
					if err != nil || string(data) != "outside\n" {
						t.Errorf("link target changed: %q, %v", data, err)
					}
				})
			}
			before, err := Fingerprint(ws, nil)
			if err != nil {
				t.Fatal(err)
			}
			c := Cell{Workspace: ws, Task: manifest.Task{Revision: pre, Provenance: manifest.Provenance{FixRevision: fix}}}
			ch := manifest.Check{ID: "fix", Type: manifest.CheckFixTests, Paths: []string{"a.go"}}
			res, checkErr := (&Options{}).check(context.Background(), c, ch, &record.Record{After: before}, src, os.Environ(), time.Second)
			if checkErr != nil || res.Passed || res.Exit != nil || !strings.Contains(res.Output, old) || !strings.Contains(res.Output, why) || res.OutputSHA256 != record.SHA256(res.Output) {
				t.Errorf("result %+v, error %v", res, checkErr)
			}
			restored, err := Fingerprint(ws, nil)
			if err != nil || restored != before {
				t.Fatalf("failed check did not restore worker: %+v, %v", restored, err)
			}
			if kind == "directory" {
				if fi, err := os.Lstat(target); err != nil || !fi.IsDir() {
					t.Fatalf("worker directory not preserved: %v", err)
				}
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
	res, err := (&Options{}).check(context.Background(), c, manifest.Check{ID: "next", Type: manifest.CheckCommand, Command: []string{self, "-test.run=^$"}}, &record.Record{After: fp}, "", os.Environ(), time.Second)
	if err != nil || res.Passed || res.Exit != nil || !strings.Contains(res.Output, "tree changed before") || res.OutputSHA256 != record.SHA256(res.Output) {
		t.Fatalf("result %+v, error %v", res, err)
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

func TestFollowupCheckHelper(t *testing.T) {
	if target := os.Getenv("BENCH_TEST_ABSENT"); target != "" {
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("worker test present: %v", err)
		}
	}
	if os.Getenv("BENCH_TEST_MUTATE") == "1" {
		if err := os.WriteFile("a.go", []byte("package changed\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckRecordsDriftAfterCommand(t *testing.T) {
	for _, kind := range []string{manifest.CheckCommand, manifest.CheckFixTests} {
		t.Run(kind, func(t *testing.T) {
			src, _, _, c := followupRepo(t)
			fp, err := Fingerprint(c.Workspace, nil)
			if err != nil {
				t.Fatal(err)
			}
			self, _ := os.Executable()
			ch := manifest.Check{ID: "mutate", Type: kind, Command: []string{self, "-test.run=^TestFollowupCheckHelper$"}}
			r := &record.Record{After: fp}
			res, err := (&Options{}).check(context.Background(), c, ch, r, src, append(os.Environ(), "BENCH_TEST_MUTATE=1"), 5*time.Second)
			if err != nil || res.Passed || record.Passes(ch, res, r) || !strings.Contains(res.Output, "tree changed after") || res.OutputSHA256 != record.SHA256(res.Output) {
				t.Fatalf("result %+v, error %v", res, err)
			}
			want := -1
			ch.Expect.Exit = &want
			if record.Passes(ch, res, r) {
				t.Fatal("drift matched expected failure exit")
			}
			next, err := (&Options{}).check(context.Background(), c, ch, r, src, os.Environ(), 5*time.Second)
			if err != nil || next.Passed || !strings.Contains(next.Output, "tree changed before") || record.Passes(ch, next, r) {
				t.Fatalf("next result %+v, error %v", next, err)
			}
		})
	}
}

func TestGitDisablesWorkerHooks(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(strconv.FormatBool(configured), func(t *testing.T) {
			_, pre, _, c := followupRepo(t)
			hooks := filepath.Join(c.Workspace, ".git", "hooks")
			if configured {
				hooks = t.TempDir()
				followupGit(t, c.Workspace, "config", "core.hooksPath", filepath.ToSlash(hooks))
			}
			if err := os.MkdirAll(hooks, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "HOOK-RAN")
			hook := filepath.Join(hooks, "reference-transaction")
			if err := os.WriteFile(hook, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			followupGit(t, c.Workspace, "commit", "--allow-empty", "-qm", "worker")
			if _, err := git(c.Workspace, "reset", "--soft", pre); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("hook ran: %v", err)
			}
		})
	}
}

func TestGitDisablesWorkerFsmonitor(t *testing.T) {
	_, _, _, c := followupRepo(t)
	marker := filepath.Join(t.TempDir(), "FSMONITOR-RAN")
	script := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	followupGit(t, c.Workspace, "config", "core.fsmonitor", filepath.ToSlash(script))
	if _, err := Fingerprint(c.Workspace, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("monitor ran: %v", err)
	}
}

func TestGitDisablesIncludedWorkerFilters(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		t.Run(strconv.FormatBool(conditional), func(t *testing.T) {
			_, _, _, c := followupRepo(t)
			config := filepath.Join(t.TempDir(), "filters")
			if err := os.WriteFile(config, []byte("[filter \"planted\"]\n clean = touch FILTER-RAN\n required = true\n"), 0600); err != nil {
				t.Fatal(err)
			}
			key := "include.path"
			if conditional {
				key = "includeIf.gitdir:**/ws/.git.path"
			}
			followupGit(t, c.Workspace, "config", key, filepath.ToSlash(config))
			if got := followupGit(t, c.Workspace, "config", "--includes", "--get", "filter.planted.clean"); got != "touch FILTER-RAN" {
				t.Fatalf("fixture filter not active: %q", got)
			}
			os.WriteFile(filepath.Join(c.Workspace, ".gitattributes"), []byte("*.go filter=planted\n"), 0600)
			os.WriteFile(filepath.Join(c.Workspace, "a.go"), []byte("package a\n// changed\n"), 0600)
			if _, err := Fingerprint(c.Workspace, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(c.Workspace, "FILTER-RAN")); !os.IsNotExist(err) {
				t.Fatalf("included filter ran: %v", err)
			}
		})
	}
}
