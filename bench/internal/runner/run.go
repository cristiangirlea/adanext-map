package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

// Options is one real execution of a manifest's plan.
type Options struct {
	Manifest *manifest.Manifest
	// Approval is the value of --i-have-approval-for-N-runs; it must equal the plan's run count.
	Approval int
	// Environ is the environment the runner checks and passes on, scrubbed of API keys.
	Environ []string
	// RecordsDir receives one <run id>.json per run; a run whose record exists is skipped, so
	// an interrupted execution resumes. WorkDir holds the workspaces, transcripts and warm cache.
	RecordsDir, WorkDir string
	// Sources maps a task's repository name to where to fetch it from (a local clone or a URL);
	// without an entry, the repository's URL.
	Sources map[string]string
	// Fake is the adapter for the fake backend (synthetic manifests only).
	Fake Fake
	// Keep leaves each workspace in place after its record is written.
	Keep bool
	Log  io.Writer
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

func (o *Options) adapter(cli string) (Adapter, error) {
	switch cli {
	case "claude":
		return Claude{}, nil
	case "fake":
		return o.Fake, nil
	}
	return nil, fmt.Errorf("no adapter for %q yet: claude is the first", cli)
}

// Execute runs every planned run that has no record yet, in plan order, after Authorize. Each run
// gets a fresh shallow checkout of its task's revision, the condition's probe, index and
// injection, up to the policy's attempts, the after fingerprint, the declared checks, and its
// record. A leak of a hidden revision or path stops the whole execution.
func Execute(ctx context.Context, o Options) error {
	m := o.Manifest
	if err := Authorize(m, o.Approval, o.Environ); err != nil {
		return err
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	for _, b := range m.Backends {
		if _, err := o.adapter(b.CLI); err != nil {
			return err
		}
	}
	for _, dir := range []string{o.RecordsDir, o.WorkDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
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
	env := scrub(o.Environ)
	for _, run := range m.Plan() {
		if _, err := os.Stat(filepath.Join(o.RecordsDir, run.ID+".json")); err == nil {
			fmt.Fprintf(o.Log, "run %s: recorded already, skipped\n", run.ID)
			continue
		}
		c := Cell{Run: run, Task: tasks[run.Task], Condition: conds[run.Condition], Backend: backends[run.Backend], Execution: m.Execution}
		fmt.Fprintf(o.Log, "run %d/%d %s\n", run.Seq, m.PlannedRuns(), run.ID)
		r, err := o.one(ctx, c, env)
		if err != nil {
			return fmt.Errorf("run %s: %w", run.ID, err)
		}
		if err := record.Write(o.RecordsDir, r); err != nil {
			return err
		}
	}
	recs, err := record.ReadDir(o.RecordsDir)
	if err != nil {
		return err
	}
	if problems := record.Check(m, recs); len(problems) > 0 {
		var ps []string
		for _, p := range problems {
			ps = append(ps, p.String())
		}
		return fmt.Errorf("the records fail their own check: %s", strings.Join(ps, "; "))
	}
	return nil
}

func (o *Options) one(ctx context.Context, c Cell, env []string) (*record.Record, error) {
	m := o.Manifest
	run := c.Run
	dir := filepath.Join(o.WorkDir, run.ID)
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	// The workspace is a directory of its own in the system temp dir, so the worker has no
	// neighbours: the other runs' transcripts and the warm cache stay in WorkDir, away from it.
	// (The worker is not sandboxed; see bench/README.md.)
	wsRoot, err := os.MkdirTemp("", "adamap-bench-ws-")
	if err != nil {
		return nil, err
	}
	c.Workspace, c.StateDir = filepath.Join(wsRoot, "ws"), filepath.Join(dir, "state")
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		return nil, err
	}
	if o.Keep {
		fmt.Fprintf(o.Log, "run %s: workspace kept at %s\n", run.ID, c.Workspace)
	} else {
		defer os.RemoveAll(wsRoot)
	}
	source := o.Sources[c.Task.Repository.Name]
	if source == "" {
		source = c.Task.Repository.URL
	}
	r := &record.Record{Format: record.Format, RunID: run.ID, Task: run.Task, Condition: run.Condition, Backend: run.Backend,
		Repeat: run.Repeat, Synthetic: m.Synthetic, Manifest: m.Name, StartedAt: o.Now().UTC().Format(time.RFC3339),
		Attempts: []record.Attempt{}, Checks: []record.CheckResult{}}
	if err := Checkout(source, c.Task.Revision, c.Workspace); err != nil {
		return nil, err
	}
	fp, err := Fingerprint(c.Workspace, nil)
	if err != nil {
		return nil, err
	}
	r.Source = fp
	if err := leaks(c); err != nil {
		return nil, err
	}
	condEnv := append(slices.Clone(env), envList(c.Condition.Env)...)
	timeout := time.Duration(m.Execution.TimeoutSeconds) * time.Second
	if len(c.Condition.Probe) > 0 {
		res := runProcess(ctx, c.Condition.Probe, c.Workspace, condEnv, "", timeout)
		if res.exit != 0 || res.err != nil {
			r.Unsupported = fmt.Sprintf("probe %q failed (exit %d): %s", strings.Join(c.Condition.Probe, " "), res.exit, firstLine(res.stderr, res.stdout))
			r.After = r.Source
			return r, nil
		}
	}
	var exclude []string
	if x := c.Condition.Index; x != nil {
		exclude = []string{x.Output}
		idx, err := o.index(ctx, c, r.Source, condEnv, timeout)
		if err != nil {
			return nil, err
		}
		r.Index = idx
	}
	if len(c.Condition.Inject) > 0 {
		res := runProcess(ctx, c.Condition.Inject, c.Workspace, condEnv, "", timeout)
		if res.exit != 0 || res.err != nil {
			return nil, fmt.Errorf("inject %q failed (exit %d): %s", strings.Join(c.Condition.Inject, " "), res.exit, firstLine(res.stderr, res.stdout))
		}
		c.Injected = string(res.stdout)
	}
	ad, _ := o.adapter(c.Backend.CLI)
	var answer string
	for n := 1; n <= m.Execution.MaxAttempts; n++ {
		c.Attempt = n
		if n > 1 {
			// A retry is a fresh session on the task's revision: drop what the failed attempt
			// left, the condition's index excepted.
			if err := reset(c.Workspace, r.Source.Commit, exclude); err != nil {
				return nil, err
			}
		}
		inv, err := ad.Command(c)
		if err != nil {
			return nil, err
		}
		for p, text := range inv.Files {
			if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
				return nil, err
			}
		}
		start := time.Now()
		res := runProcess(ctx, inv.Argv, inv.Dir, append(slices.Concat(env, inv.Env), "ADAMAP_BENCH_ATTEMPT="+strconv.Itoa(n)), inv.Stdin, inv.Timeout)
		wall := time.Since(start).Milliseconds()
		transcript := filepath.Join(c.StateDir, fmt.Sprintf("attempt-%d.jsonl", n))
		if err := os.WriteFile(transcript, res.stdout, 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(c.StateDir, fmt.Sprintf("attempt-%d.stderr", n)), res.stderr, 0o644); err != nil {
			return nil, err
		}
		out := ad.Parse(res.stdout, c.Backend)
		a := record.Attempt{N: n, WallMS: wall, Exit: res.exit, TimedOut: res.timedOut, SessionID: out.SessionID,
			Usage: out.Usage, Cost: out.Cost, Observed: out.Observed, OutputBytes: int64(len(res.stdout)),
			TranscriptSHA256: record.SHA256(string(res.stdout))}
		a.Observed.InjectedBytes = int64(len(c.Injected))
		switch {
		case res.err != nil:
			a.Error = res.err.Error()
		case out.Err != "":
			a.Error = out.Err
		case res.exit != 0:
			a.Error = "exit status " + strconv.Itoa(res.exit)
		}
		r.Attempts = append(r.Attempts, a)
		answer = out.Answer
		if a.Completed() {
			break
		}
	}
	r.Answer, r.AnswerSHA256 = answer, record.SHA256(answer)
	// A worker that committed moved HEAD: put it back, keeping the tree and the index, so the
	// fingerprint and the checks see every change against the task's revision.
	if head, err := git(c.Workspace, "rev-parse", "HEAD"); err == nil && strings.TrimSpace(head) != r.Source.Commit {
		r.WorkerHead = strings.TrimSpace(head)
		if _, err := git(c.Workspace, "reset", "-q", "--soft", r.Source.Commit); err != nil {
			return nil, err
		}
	}
	after, err := Fingerprint(c.Workspace, exclude)
	if err != nil {
		return nil, err
	}
	r.After = after
	r.Isolation = record.Isolation{Checked: true}
	for _, rev := range c.Task.Leakage.HiddenRevisions {
		if Reachable(c.Workspace, rev) {
			r.Isolation.Violations = append(r.Isolation.Violations, "hidden revision "+rev+" became reachable from the workspace")
		}
	}
	checkTimeout := timeout
	if s := m.Execution.CheckTimeoutSeconds; s > 0 {
		checkTimeout = time.Duration(s) * time.Second
	}
	for _, ch := range c.Task.Checks {
		res, err := o.check(ctx, c, ch, r, source, env, checkTimeout)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", ch.ID, err)
		}
		r.Checks = append(r.Checks, res)
	}
	return r, nil
}

// leaks stops a run whose workspace shows what the worker must not see.
func leaks(c Cell) error {
	var found []string
	for _, rev := range c.Task.Leakage.HiddenRevisions {
		if Reachable(c.Workspace, rev) {
			found = append(found, "hidden revision "+rev+" is reachable")
		}
	}
	for _, p := range c.Task.Leakage.HiddenPaths {
		if _, err := os.Lstat(filepath.Join(c.Workspace, filepath.FromSlash(p))); err == nil {
			found = append(found, "hidden path "+p+" exists")
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("leakage in the fresh workspace, fix the task before any run: %s", strings.Join(found, "; "))
	}
	return nil
}

// indexMeta is what the warm cache keeps beside a built index: the tree it was built from.
type indexMeta struct {
	Digest  string `json:"digest"`
	Commit  string `json:"commit"`
	Content string `json:"content"`
	Bytes   int64  `json:"bytes"`
}

// index builds the condition's index in the workspace (and, warm, keeps a copy for the cell's
// later repeats), or puts the cell's built copy back.
func (o *Options) index(ctx context.Context, c Cell, source record.Fingerprint, env []string, timeout time.Duration) (*record.Index, error) {
	x := c.Condition.Index
	out := filepath.Join(c.Workspace, filepath.FromSlash(x.Output))
	cache := filepath.Join(o.WorkDir, "cache", c.Task.ID+"."+c.Condition.ID+"."+c.Backend.ID)
	if c.Run.Index == "reuse" {
		var meta indexMeta
		data, err := os.ReadFile(filepath.Join(cache, "meta.json"))
		if err != nil {
			return nil, fmt.Errorf("no built index to reuse: %w", err)
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, err
		}
		if err := copyTree(filepath.Join(cache, "index"), out); err != nil {
			return nil, err
		}
		digest, size, err := digestTree(out)
		if err != nil {
			return nil, err
		}
		return &record.Index{Digest: digest, Commit: meta.Commit, Content: meta.Content, Settings: x.Settings, Reused: true, Bytes: size}, nil
	}
	start := time.Now()
	res := runProcess(ctx, x.Command, c.Workspace, env, "", timeout)
	ms := time.Since(start).Milliseconds()
	if res.exit != 0 || res.err != nil {
		return nil, fmt.Errorf("index %q failed (exit %d): %s", strings.Join(x.Command, " "), res.exit, firstLine(res.stderr, res.stdout))
	}
	digest, size, err := digestTree(out)
	if err != nil {
		return nil, fmt.Errorf("index output: %w", err)
	}
	if c.Run.Cache == "warm" {
		if err := os.RemoveAll(cache); err != nil {
			return nil, err
		}
		if err := copyTree(out, filepath.Join(cache, "index")); err != nil {
			return nil, err
		}
		meta, _ := json.Marshal(indexMeta{Digest: digest, Commit: source.Commit, Content: source.Content, Bytes: size})
		if err := os.WriteFile(filepath.Join(cache, "meta.json"), meta, 0o644); err != nil {
			return nil, err
		}
	}
	return &record.Index{Digest: digest, Commit: source.Commit, Content: source.Content, Settings: x.Settings, BuildMS: &ms, Bytes: size}, nil
}

// check runs one declared check on the tree the worker left and grades it with record.Passes.
func (o *Options) check(ctx context.Context, c Cell, ch manifest.Check, r *record.Record, source string, env []string, timeout time.Duration) (record.CheckResult, error) {
	res := record.CheckResult{ID: ch.ID, Type: ch.Type, Content: r.After.Content}
	switch ch.Type {
	case manifest.CheckCommand, manifest.CheckFixTests:
		var head string
		if ch.Type == manifest.CheckFixTests {
			fix := c.Task.Provenance.FixRevision
			files, err := o.overlay(c, source, fix, ch.Paths)
			if err != nil {
				return res, err
			}
			res.Overlay = fix
			head = "overlay from " + fix + ": " + strings.Join(files, ", ") + "\n"
		}
		p := runProcess(ctx, ch.Command, c.Workspace, env, "", timeout)
		if p.err != nil && p.exit == 0 {
			p.exit = -1
		}
		exit := p.exit
		res.Exit = &exit
		res.Output = head + string(p.stdout) + string(p.stderr)
	case manifest.CheckAnswer, manifest.CheckReview:
		res.Output = r.Answer
	case manifest.CheckNoChanges:
		res.Output = "content " + r.After.Content + "\n"
	}
	res.OutputSHA256 = record.SHA256(res.Output)
	if ch.Type != manifest.CheckReview {
		res.Passed = record.Passes(ch, res, r)
	}
	return res, nil
}

// overlay lays the fix's matching files over the workspace, from a grader checkout made outside
// it after the worker finished.
func (o *Options) overlay(c Cell, source, fix string, patterns []string) ([]string, error) {
	// The grader's checkout holds the fix: it never stays behind, --keep or not.
	root, err := os.MkdirTemp("", "adamap-bench-grader-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	grader := filepath.Join(root, "grader")
	if err := Checkout(source, fix, grader); err != nil {
		return nil, err
	}
	if err := fetch(grader, source, c.Task.Revision); err != nil {
		return nil, err
	}
	files, err := fixFiles(grader, c.Task.Revision, fix, patterns)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("the fix %s changes no file matching %v", fix, patterns)
	}
	for _, f := range files {
		if err := copyFile(filepath.Join(grader, filepath.FromSlash(f)), filepath.Join(c.Workspace, filepath.FromSlash(f))); err != nil {
			return nil, err
		}
	}
	return files, nil
}

type processResult struct {
	stdout, stderr []byte
	exit           int
	timedOut       bool
	err            error
}

// runProcess runs argv in dir with exactly env, stdin, and a timeout; a program that cannot start
// is an error with exit -1.
func runProcess(ctx context.Context, argv []string, dir string, env []string, stdin string, timeout time.Duration) processResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = 10 * time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	res := processResult{stdout: out.Bytes(), stderr: errOut.Bytes()}
	var exit *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.timedOut, res.exit = true, -1
		res.err = fmt.Errorf("timed out after %s", timeout)
	case errors.As(err, &exit):
		res.exit = exit.ExitCode()
	case err != nil:
		res.exit, res.err = -1, err
	}
	return res
}

func firstLine(texts ...[]byte) string {
	for _, t := range texts {
		if s := strings.TrimSpace(string(t)); s != "" {
			line, _, _ := strings.Cut(s, "\n")
			return line
		}
	}
	return "no output"
}
