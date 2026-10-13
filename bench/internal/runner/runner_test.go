package runner_test

import (
	"bytes"
	"context"
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
	"github.com/cristiangirlea/adanext-map/bench/internal/reduce"
	"github.com/cristiangirlea/adanext-map/bench/internal/runner"
)

// The test binary stands in for the programs a run starts: the fake agent, an indexer, a probe
// that fails, and a check that passes only when the fix's test file was laid over the tree.
func TestMain(m *testing.M) {
	role := os.Getenv("BENCH_TEST_HELPER")
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "bench-helper-") {
		role = strings.TrimPrefix(os.Args[1], "bench-helper-") // an indexer or a probe, named by its argv
	}
	switch role {
	case "agent":
		attempt, _ := strconv.Atoi(os.Getenv("ADAMAP_BENCH_ATTEMPT"))
		failUntil, _ := strconv.Atoi(os.Getenv("BENCH_TEST_FAIL"))
		os.Exit(runner.FakeAgent(os.Stdin, os.Stdout, attempt, failUntil))
	case "agent-commit": // a worker that commits its change
		os.WriteFile("b.go", []byte("package a\n\nfunc B() {}\n"), 0o644)
		for _, args := range [][]string{{"add", "b.go"}, {"-c", "user.name=w", "-c", "user.email=w@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "worker"}} {
			if err := exec.Command("git", args...).Run(); err != nil {
				os.Exit(1)
			}
		}
		attempt, _ := strconv.Atoi(os.Getenv("ADAMAP_BENCH_ATTEMPT"))
		os.Exit(runner.FakeAgent(os.Stdin, os.Stdout, attempt, 0))
	case "agent-junk": // a worker whose first attempt leaves a file behind and fails
		attempt, _ := strconv.Atoi(os.Getenv("ADAMAP_BENCH_ATTEMPT"))
		if attempt == 1 {
			os.WriteFile("junk.txt", []byte("half done\n"), 0o644)
			os.WriteFile("a.go", []byte("package broken\n"), 0o644)
			os.Exit(1)
		}
		if p := os.Getenv("BENCH_TEST_EXPECT"); p != "" {
			if _, err := os.Stat(p); err != nil {
				os.Exit(1) // the condition's index is gone
			}
		}
		os.Exit(runner.FakeAgent(os.Stdin, os.Stdout, attempt, 0))
	case "agent-fetch": // a worker that fetches the hidden fix
		cmd := exec.Command("git", "fetch", "-q", "--upload-pack", "git -c uploadpack.allowAnySHA1InWant=true upload-pack",
			os.Getenv("BENCH_TEST_SOURCE"), os.Getenv("BENCH_TEST_FIX"))
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		attempt, _ := strconv.Atoi(os.Getenv("ADAMAP_BENCH_ATTEMPT"))
		os.Exit(runner.FakeAgent(os.Stdin, os.Stdout, attempt, 0))
	case "sleep":
		time.Sleep(20 * time.Second)
		os.Exit(0)
	case "agent-billing": // a CLI that announces API billing, then works on
		os.Stdout.WriteString(`{"type":"system","subtype":"init","session_id":"b","apiKeySource":"ANTHROPIC_API_KEY"}` + "\n")
		time.Sleep(20 * time.Second)
		os.Exit(0)
	case "agent-nosource": // a CLI whose init event does not say its login
		os.Stdout.WriteString(`{"type":"system","subtype":"init","session_id":"n"}` + "\n")
		time.Sleep(20 * time.Second)
		os.Exit(0)
	case "agent-where": // a worker that says where it runs
		wd, _ := os.Getwd()
		f, err := os.OpenFile(os.Getenv("BENCH_TEST_WHERE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(1)
		}
		f.WriteString(wd + "\n")
		f.Close()
		attempt, _ := strconv.Atoi(os.Getenv("ADAMAP_BENCH_ATTEMPT"))
		os.Exit(runner.FakeAgent(os.Stdin, os.Stdout, attempt, 0))
	case "index":
		if err := os.MkdirAll(".idx", 0o755); err != nil {
			os.Exit(1)
		}
		data, _ := os.ReadFile("a.go")
		if err := os.WriteFile(filepath.Join(".idx", "data"), append([]byte("index of "), data...), 0o644); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "fail":
		os.Exit(3)
	case "check":
		if _, err := os.Stat("a_test.go"); err != nil {
			os.Stdout.WriteString("no a_test.go\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("a_test.go present\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a source repository with a pre-fix commit and a fix that adds a test.
func repo(t *testing.T) (dir, pre, fix string) {
	t.Helper()
	needGit(t)
	dir = t.TempDir()
	gitIn(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "pre")
	pre = gitIn(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, "a.go"), "package a\n\nfunc A() {}\n")
	write(t, filepath.Join(dir, "a_test.go"), "package a\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "fix")
	fix = gitIn(t, dir, "rev-parse", "HEAD")
	return dir, pre, fix
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// smoke is a synthetic smoke over the repo: a navigation task and a fix task, under none and
// adamap (with an index), two repeats, warm.
func smoke(t *testing.T, pre, fix string) *manifest.Manifest {
	// Children of this test (the checks) act as the check helper unless told otherwise.
	t.Setenv("BENCH_TEST_HELPER", "check")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := benchtest.Synthetic()
	m.Scale = "smoke"
	m.Repeats = 2
	m.Budget.MaxRuns = 8
	m.Execution.TimeoutSeconds = 60
	m.Adoption.MinTasks = 1
	m.Conditions = []manifest.Condition{m.Conditions[0], m.Conditions[4]}
	m.Conditions[1].Index = &manifest.Index{Command: []string{self, "bench-helper-index"}, Output: ".idx"}
	nav := benchtest.Task(0, manifest.TaskNavigation)
	nav.Prompt = "Which file defines package a? pkg/a.go and pkg/b.go"
	nav.Repository = manifest.Repository{Name: "local/a", URL: "unused", Visibility: "private"}
	nav.Revision = pre
	fx := benchtest.Task(1, manifest.TaskFix)
	fx.Prompt = "Add func A."
	fx.Repository = nav.Repository
	fx.Revision = pre
	fx.Provenance.FixRevision = fix
	fx.Leakage.HiddenRevisions = []string{fix}
	fx.Checks[0].Command = []string{self}
	m.Tasks = []manifest.Task{nav, fx}
	return m
}

func options(t *testing.T, m *manifest.Manifest, source string, approval int) runner.Options {
	self, _ := os.Executable()
	return runner.Options{
		Manifest: m, Approval: approval, Environ: cleanEnviron(),
		RecordsDir: filepath.Join(t.TempDir(), "records"), WorkDir: filepath.Join(t.TempDir(), "work"),
		Sources: map[string]string{"local/a": source},
		Fake:    runner.Fake{Program: []string{self}, Env: []string{"BENCH_TEST_HELPER=agent"}},
		Log:     &bytes.Buffer{},
	}
}

// cleanEnviron is this process's environment without the provider variables a real run refuses,
// so the tests do not depend on the machine's own.
func cleanEnviron() []string {
	var out []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !manifest.IsProviderKeyVar(k) {
			out = append(out, kv)
		}
	}
	return out
}

func TestExecuteRefusesWithoutMatchingApproval(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	for _, n := range []int{0, 7, 9} {
		o := options(t, m, src, n)
		err := runner.Execute(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "--i-have-approval-for-N-runs 8") {
			t.Fatalf("approval %d: err %v", n, err)
		}
		if _, err := os.Stat(o.WorkDir); err == nil {
			t.Fatalf("approval %d: a workspace was made", n)
		}
	}
}

func TestExecuteRefusesProviderKeys(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	o := options(t, m, src, 8)
	o.Environ = append(o.Environ, "ANTHROPIC_API_KEY=sk-secret-value", "OPENAI_API_KEY=x")
	err := runner.Execute(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("the error prints the key: %v", err)
	}
	if got := runner.ProviderKeysSet([]string{"ANTHROPIC_API_KEY=", "GH_TOKEN=x", "GEMINI_API_KEY=y"}); strings.Join(got, ",") != "GEMINI_API_KEY" {
		t.Fatalf("ProviderKeysSet = %v", got)
	}
}

func TestExecuteRefusesAManifestWithBlockers(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	m.Tasks[0].Status = "candidate"
	err := runner.Execute(context.Background(), options(t, m, src, 8))
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("err %v", err)
	}
}

func TestExecuteRunsEveryPlannedCellWithTheFakeAdapter(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	o := options(t, m, src, 8)
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatalf("%v\nlog:\n%s", err, o.Log)
	}
	recs, err := record.ReadDir(o.RecordsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 8 {
		t.Fatalf("%d records", len(recs))
	}
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
	for _, r := range recs {
		if !r.Synthetic || !record.Success(m, &r) {
			t.Errorf("%s: synthetic %v success %v, checks %+v attempts %+v", r.RunID, r.Synthetic, record.Success(m, &r), r.Checks, r.Attempts)
		}
		if tok, ok := r.Attempts[0].Usage.Tokens(); !ok || tok.Total == 0 {
			t.Errorf("%s: usage %+v", r.RunID, r.Attempts[0].Usage)
		}
		obs := r.Attempts[0].Observed
		if !obs.Observable || obs.ToolCalls["Read"] != 1 || obs.SourceReads == nil || *obs.SourceReads != 1 || obs.SourceBytes == nil || *obs.SourceBytes == 0 {
			t.Errorf("%s: observed %+v", r.RunID, obs)
		}
	}
	build := benchtest.Find(recs, "t01.adamap.fake.r1")
	reuse := benchtest.Find(recs, "t01.adamap.fake.r2")
	if build.Index == nil || build.Index.Reused || build.Index.BuildMS == nil || reuse.Index == nil || !reuse.Index.Reused ||
		reuse.Index.Digest != build.Index.Digest {
		t.Fatalf("index build %+v reuse %+v", build.Index, reuse.Index)
	}
	fx := benchtest.Find(recs, "t01.none.fake.r1")
	if fx.Checks[0].Overlay != fix || !strings.Contains(fx.Checks[0].Output, "a_test.go present") {
		t.Fatalf("fix-tests %+v", fx.Checks[0])
	}
	if fx.After.Content != record.CleanContent {
		t.Fatalf("the fake agent changed nothing, yet the tree differs: %s", fx.After.Content)
	}
	rep, err := reduce.Reduce(m, recs)
	if err != nil {
		t.Fatal(err)
	}
	if md := rep.Markdown(); !strings.Contains(md, "SYNTHETIC") {
		t.Fatal("a fake run's report is not labelled synthetic")
	}
	// The workspaces go; each run's state (its transcripts) and the warm cache stay.
	for _, r := range recs {
		if _, err := os.Stat(filepath.Join(o.WorkDir, r.RunID, "ws")); err == nil {
			t.Fatalf("%s: workspace left behind", r.RunID)
		}
		data, err := os.ReadFile(filepath.Join(o.WorkDir, r.RunID, "state", "attempt-1.jsonl"))
		if err != nil || record.SHA256(string(data)) != r.Attempts[0].TranscriptSHA256 {
			t.Fatalf("%s: transcript %v does not match its hash", r.RunID, err)
		}
	}
}

func TestExecuteRetriesAFailedAttempt(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	o := options(t, m, src, 8)
	o.Fake.Env = append(o.Fake.Env, "BENCH_TEST_FAIL=1")
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	recs, _ := record.ReadDir(o.RecordsDir)
	for _, r := range recs {
		if len(r.Attempts) != 2 || r.Attempts[0].Completed() || !r.Attempts[1].Completed() {
			t.Fatalf("%s: attempts %+v", r.RunID, r.Attempts)
		}
		if r.Attempts[0].Usage.Source != record.Unavailable {
			t.Fatalf("%s: a failed attempt without a result reports usage %+v", r.RunID, r.Attempts[0].Usage)
		}
	}
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
}

func TestExecuteRecordsAnUnsupportedCondition(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	self, _ := os.Executable()
	lsp := benchtest.Synthetic().Conditions[1]
	lsp.Probe = []string{self, "bench-helper-fail"}
	m.Conditions = append(m.Conditions, lsp)
	m.Budget.MaxRuns = 12
	o := options(t, m, src, 12)
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	recs, _ := record.ReadDir(o.RecordsDir)
	r := benchtest.Find(recs, "t00.lsp.fake.r1")
	if r.Unsupported == "" || len(r.Attempts) != 0 || record.Success(m, r) {
		t.Fatalf("unsupported run %+v", r)
	}
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
}

// The workspace holds the pre-fix revision only: the fix is not reachable from it, and its files
// are absent. Its fingerprint is clean until it changes, and excluded paths do not count.
func TestCheckoutHidesTheFixAndFingerprintSeesEdits(t *testing.T) {
	src, pre, fix := repo(t)
	ws := filepath.Join(t.TempDir(), "ws")
	if err := runner.Checkout(src, pre, ws); err != nil {
		t.Fatal(err)
	}
	if runner.Reachable(ws, fix) {
		t.Fatal("the fix revision is reachable from the workspace")
	}
	if !runner.Reachable(ws, pre) {
		t.Fatal("the revision itself is not reachable")
	}
	if _, err := os.Stat(filepath.Join(ws, "a_test.go")); err == nil {
		t.Fatal("the fix's test file is in the workspace")
	}
	fp, err := runner.Fingerprint(ws, nil)
	if err != nil || fp.Commit != pre || fp.Content != record.CleanContent {
		t.Fatalf("fingerprint %+v, %v", fp, err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws, ".idx", "x"), "index")
	if fp, _ := runner.Fingerprint(ws, []string{".idx"}); fp.Content != record.CleanContent {
		t.Fatal("an excluded path changes the fingerprint")
	}
	write(t, filepath.Join(ws, "a.go"), "package a // edited\n")
	edited, _ := runner.Fingerprint(ws, []string{".idx"})
	write(t, filepath.Join(ws, "a.go"), "package a // edited again\n")
	again, _ := runner.Fingerprint(ws, []string{".idx"})
	if edited.Content == record.CleanContent || edited.Content == again.Content || edited.Commit != pre {
		t.Fatal("the fingerprint does not follow the content of a dirty tree")
	}
}

func TestDryRunPrintsEveryPlannedCellAndRunsNothing(t *testing.T) {
	m := benchtest.Pilot()
	var out bytes.Buffer
	runner.DryRun(m, &out, runner.Fake{}, []string{"ANTHROPIC_API_KEY=sk-x", "PATH=/bin"})
	text := out.String()
	for _, want := range []string{"150 planned runs", "--i-have-approval-for-N-runs 150", "ANTHROPIC_API_KEY", "t09.adamap.claude.r3", "claude -p"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in the dry run", want)
		}
	}
	if strings.Contains(text, "sk-x") {
		t.Error("the dry run prints a key")
	}
	runs := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "run ") {
			runs++
		}
	}
	if runs != 150 {
		t.Fatalf("%d run lines", runs)
	}
	out.Reset()
	runner.DryRun(benchtest.Synthetic(), &out, runner.Fake{Program: []string{"adamap-bench", "fake-agent"}}, nil)
	if !strings.Contains(out.String(), "SYNTHETIC") {
		t.Fatal("a synthetic dry run is not labelled")
	}
}

// A worker that commits leaves HEAD elsewhere; the runner puts HEAD back on the task's revision,
// keeping the tree, so the checks see the change and the record still matches its source.
func TestExecuteUndoesAWorkersCommit(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	m.Tasks[0].Checks = append(m.Tasks[0].Checks, manifest.Check{ID: "untouched", Type: manifest.CheckNoChanges})
	o := options(t, m, src, 8)
	o.Fake.Env = []string{"BENCH_TEST_HELPER=agent-commit"}
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	recs, _ := record.ReadDir(o.RecordsDir)
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
	r := benchtest.Find(recs, "t00.none.fake.r1")
	if r.After.Commit != pre || r.After.Content == record.CleanContent || r.WorkerHead == "" || r.WorkerHead == pre {
		t.Fatalf("after %+v, worker head %q", r.After, r.WorkerHead)
	}
	for _, c := range r.Checks {
		if c.ID == "untouched" && c.Passed {
			t.Fatal("no-changes passed on a committed change")
		}
	}
}

func TestCheckoutRefusesASourceReadAsAnOption(t *testing.T) {
	needGit(t)
	err := runner.Checkout("--upload-pack=touch pwned", benchtest.Rev(1), filepath.Join(t.TempDir(), "ws"))
	if err == nil || !strings.Contains(err.Error(), "option") {
		t.Fatalf("err %v", err)
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The worker runs in a directory of its own, away from the work directory that holds the other
// runs' transcripts and the warm cache; the grader's checkout of the fix never stays, --keep or not.
func TestTheWorkspaceIsAwayFromTheOtherRunsState(t *testing.T) {
	src, pre, fix := repo(t)
	// A temp directory of this test's own, so another run on the machine cannot leave its
	// workspaces or grader checkouts where this test looks.
	tmp := t.TempDir()
	for _, v := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(v, tmp)
	}
	for _, keep := range []bool{false, true} {
		m := smoke(t, pre, fix)
		o := options(t, m, src, 8)
		o.Keep = keep
		where := filepath.Join(t.TempDir(), "where.txt")
		o.Fake.Env = []string{"BENCH_TEST_HELPER=agent-where", "BENCH_TEST_WHERE=" + where}
		if err := runner.Execute(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(where)
		if err != nil {
			t.Fatal(err)
		}
		work := realPath(t, o.WorkDir)
		dirs := strings.Fields(string(data))
		if len(dirs) != 8 {
			t.Fatalf("keep %v: %d workspaces", keep, len(dirs))
		}
		for _, d := range dirs {
			if keep {
				d = realPath(t, d)
				if rel, err := filepath.Rel(work, d); err == nil && !strings.HasPrefix(rel, "..") {
					t.Fatalf("workspace %s is inside the work directory %s", d, work)
				}
				siblings, _ := os.ReadDir(filepath.Dir(d))
				if len(siblings) != 1 {
					t.Fatalf("the workspace has neighbours: %v", siblings)
				}
				os.RemoveAll(filepath.Dir(d))
			} else if _, err := os.Stat(d); err == nil {
				t.Fatalf("workspace %s left behind", d)
			}
		}
		if matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "adamap-bench-grader-*")); len(matches) > 0 {
			t.Fatalf("keep %v: grader checkouts left behind: %v", keep, matches)
		}
	}
}

// Each attempt is a fresh session on the task's revision: a retry does not inherit what the
// failed attempt left in the tree, and the condition's index stays.
func TestARetryStartsFromACleanTree(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	o := options(t, m, src, 8)
	o.Fake.Env = []string{"BENCH_TEST_HELPER=agent-junk"}
	m.Conditions[1].Env = map[string]string{"BENCH_TEST_EXPECT": ".idx"}
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	recs, _ := record.ReadDir(o.RecordsDir)
	for _, r := range recs {
		if len(r.Attempts) != 2 || !r.Attempts[1].Completed() || r.After.Content != record.CleanContent {
			t.Fatalf("%s: %d attempts, after %s", r.RunID, len(r.Attempts), r.After.Content)
		}
	}
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
}

// A hidden path or a hidden revision in the fresh workspace is a broken task: the execution stops
// before any worker runs, and no record is written.
func TestExecuteStopsOnALeakInTheFreshWorkspace(t *testing.T) {
	src, pre, fix := repo(t)
	for name, mutate := range map[string]func(m *manifest.Manifest){
		"a hidden path":     func(m *manifest.Manifest) { m.Tasks[0].Leakage.HiddenPaths = []string{"a.go"} },
		"a hidden revision": func(m *manifest.Manifest) { m.Tasks[0].Leakage.HiddenRevisions = []string{pre} },
	} {
		m := smoke(t, pre, fix)
		mutate(m)
		o := options(t, m, src, 8)
		err := runner.Execute(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "leakage") {
			t.Fatalf("%s: err %v", name, err)
		}
		if recs, _ := record.ReadDir(o.RecordsDir); len(recs) != 0 {
			t.Fatalf("%s: %d records written", name, len(recs))
		}
	}
}

// A worker that fetches the hidden fix is caught after the run: an isolation violation, so the
// run is no success.
func TestExecuteRecordsAFetchedFixAsAnIsolationFailure(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	o := options(t, m, src, 8)
	o.Fake.Env = []string{"BENCH_TEST_HELPER=agent-fetch", "BENCH_TEST_SOURCE=" + src, "BENCH_TEST_FIX=" + fix}
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	recs, _ := record.ReadDir(o.RecordsDir)
	r := benchtest.Find(recs, "t01.none.fake.r1")
	if len(r.Isolation.Violations) == 0 || !strings.Contains(r.Isolation.Violations[0], fix) || record.Success(m, r) {
		t.Fatalf("isolation %+v, success %v", r.Isolation, record.Success(m, r))
	}
}

// An interrupted execution resumes: a run with a record is skipped, a missing one runs.
func TestExecuteResumesFromTheRecordsItHas(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	o := options(t, m, src, 8)
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(o.RecordsDir, "t01.adamap.fake.r2.json")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	log := &bytes.Buffer{}
	o.Log = log
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(log.String(), "recorded already, skipped"); n != 7 {
		t.Fatalf("%d runs skipped, want 7:\n%s", n, log)
	}
	if _, err := os.Stat(gone); err != nil {
		t.Fatal("the missing run was not run again")
	}
}

func TestExecuteStopsWhenTheInjectionFails(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	self, _ := os.Executable()
	m.Conditions[1].Inject = []string{self, "bench-helper-fail"}
	err := runner.Execute(context.Background(), options(t, m, src, 8))
	if err == nil || !strings.Contains(err.Error(), "inject") {
		t.Fatalf("err %v", err)
	}
}

// A check that outlives check_timeout_seconds is stopped and fails; the record stays consistent.
func TestACheckThatTimesOutFails(t *testing.T) {
	src, pre, fix := repo(t)
	m := smoke(t, pre, fix)
	self, _ := os.Executable()
	m.Tasks[1].Checks[0].Command = []string{self, "bench-helper-sleep"}
	m.Execution.CheckTimeoutSeconds = 1
	o := options(t, m, src, 8)
	if err := runner.Execute(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	recs, _ := record.ReadDir(o.RecordsDir)
	for _, id := range []string{"t01.none.fake.r1", "t01.adamap.fake.r2"} {
		c := benchtest.Find(recs, id).Checks[0]
		if c.Passed || c.Exit == nil || *c.Exit != -1 {
			t.Fatalf("%s: check %+v", id, c)
		}
	}
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
}

// A CLI that is not on the subscription login is stopped at its init event, before a turn: the
// process is killed, no record is written, and the execution stops.
func TestExecuteStopsACLIThatIsNotOnTheSubscriptionLogin(t *testing.T) {
	src, pre, fix := repo(t)
	for _, helper := range []string{"agent-billing", "agent-nosource"} {
		m := smoke(t, pre, fix)
		o := options(t, m, src, 8)
		o.Fake.Env = []string{"BENCH_TEST_HELPER=" + helper}
		start := time.Now()
		err := runner.Execute(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "apiKeySource") {
			t.Fatalf("%s: err %v", helper, err)
		}
		if d := time.Since(start); d > 15*time.Second {
			t.Fatalf("%s: the CLI was not stopped (%s)", helper, d)
		}
		if recs, _ := record.ReadDir(o.RecordsDir); len(recs) != 0 {
			t.Fatalf("%s: %d records written", helper, len(recs))
		}
	}
}
