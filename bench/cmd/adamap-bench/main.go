// Command adamap-bench is adamap's benchmark (issue #6): it validates an experiment manifest and
// its records, prints the plan (run --dry-run), runs it through the coding CLIs on their
// subscription plans only with the owner's approval of the exact run count, and reduces the
// records into bench/RESULTS.md. bench/README.md describes the workflow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
	"github.com/cristiangirlea/adanext-map/bench/internal/reduce"
	"github.com/cristiangirlea/adanext-map/bench/internal/runner"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fake-agent" {
		os.Exit(fakeAgent(os.Stdin, os.Stdout, os.Environ()))
	}
	os.Exit(run(os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

// run is main without the process: the arguments, the environment a real run is checked
// against, the two streams, and the exit code.
func run(args, environ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "run":
		return runRun(args[1:], environ, stdout, stderr)
	case "reduce":
		return runReduce(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "adamap-bench: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: adamap-bench <command>

commands:
  validate  check a manifest (--manifest), a tasks file (--tasks), and records (--records)
  run       print every planned cell (--dry-run), or run the plan with the owner's approval:
            --manifest M --records DIR --i-have-approval-for-N-runs N
  reduce    turn checked records into the report (--manifest M --records DIR [--out FILE])
  help      print this text

A real run refuses unless N is the plan's run count and no provider API key variable is set.
bench/README.md describes the workflow and the formats.
`)
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fl := flag.NewFlagSet("adamap-bench "+name, flag.ContinueOnError)
	fl.SetOutput(stderr)
	return fl
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fl := newFlags("validate", stderr)
	mpath := fl.String("manifest", "", "the manifest to check")
	tpath := fl.String("tasks", "", "a tasks file to check on its own")
	rdir := fl.String("records", "", "a records directory to check against the manifest")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if (*mpath == "") == (*tpath == "") {
		fmt.Fprintln(stderr, "adamap-bench validate: give --manifest or --tasks")
		return 2
	}
	bad := false
	if *tpath != "" {
		f, err := manifest.LoadTasks(*tpath)
		if err != nil {
			fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
			return 1
		}
		issues := manifest.ValidateTasks(f)
		printIssues(stdout, issues)
		if len(issues) == 0 {
			fmt.Fprintf(stdout, "%s: %d tasks, valid\n", *tpath, len(f.Tasks))
		}
		return exitFor(len(issues) > 0)
	}
	m, err := manifest.Load(*mpath)
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	label := ""
	if m.Synthetic {
		label = " (SYNTHETIC fixture: not measurements)"
	}
	issues := m.Validate()
	printIssues(stdout, issues)
	if len(issues) == 0 {
		fmt.Fprintf(stdout, "%s: manifest %s%s, %d planned runs, valid\n", *mpath, m.Name, label, m.PlannedRuns())
	} else {
		bad = true
		fmt.Fprintf(stdout, "%s: manifest %s%s, %d issues\n", *mpath, m.Name, label, len(issues))
	}
	if *rdir != "" {
		recs, err := record.ReadDir(*rdir)
		if err != nil {
			fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
			return 1
		}
		problems := record.Check(m, recs)
		for _, p := range problems {
			fmt.Fprintf(stdout, "record %s\n", p)
		}
		fmt.Fprintf(stdout, "%s: %d records, %d problems\n", *rdir, len(recs), len(problems))
		bad = bad || len(problems) > 0
	}
	return exitFor(bad)
}

func printIssues(w io.Writer, issues []manifest.Issue) {
	for _, i := range issues {
		kind := "structural"
		if i.Blocker {
			kind = "blocks a run"
		}
		fmt.Fprintf(w, "issue (%s): %s\n", kind, i)
	}
}

func exitFor(bad bool) int {
	if bad {
		return 1
	}
	return 0
}

// sources collects --source name=path flags.
type sources map[string]string

func (s sources) String() string { return "" }
func (s sources) Set(v string) error {
	name, p, ok := strings.Cut(v, "=")
	if !ok || name == "" || p == "" {
		return errors.New("want name=path-or-url")
	}
	s[name] = p
	return nil
}

func runRun(args, environ []string, stdout, stderr io.Writer) int {
	fl := newFlags("run", stderr)
	mpath := fl.String("manifest", "", "the manifest")
	dry := fl.Bool("dry-run", false, "print every planned cell and the command it would run; run nothing")
	rdir := fl.String("records", "", "the directory for the run records (a real run)")
	work := fl.String("work", "", "the directory for workspaces, transcripts and the warm cache (default: a temp directory)")
	approval := fl.Int(strings.TrimPrefix(runner.ApprovalFlag, "--"), 0, "the plan's run count, as the owner approved it")
	keep := fl.Bool("keep", false, "leave each workspace in place")
	src := sources{}
	fl.Var(src, "source", "where to fetch a repository from, as name=path-or-url (repeatable)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *mpath == "" {
		fmt.Fprintln(stderr, "adamap-bench run: --manifest is required")
		return 2
	}
	m, err := manifest.Load(*mpath)
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	fake := runner.Fake{Program: []string{self, "fake-agent"}}
	if *dry {
		runner.DryRun(m, stdout, fake, environ)
		return 0
	}
	if *rdir == "" {
		fmt.Fprintln(stderr, "adamap-bench run: a real run needs --records DIR (or use --dry-run)")
		return 2
	}
	if *work == "" {
		*work = filepath.Join(os.TempDir(), "adamap-bench", m.Name)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = runner.Execute(ctx, runner.Options{Manifest: m, Approval: *approval, Environ: environ,
		RecordsDir: *rdir, WorkDir: *work, Sources: src, Fake: fake, Keep: *keep, Log: stderr})
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%d runs recorded in %s\n", m.PlannedRuns(), *rdir)
	return 0
}

func runReduce(args []string, stdout, stderr io.Writer) int {
	fl := newFlags("reduce", stderr)
	mpath := fl.String("manifest", "", "the manifest")
	rdir := fl.String("records", "", "the records directory")
	out := fl.String("out", "", "write the report here instead of stdout (bench/RESULTS.md for a real pilot)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *mpath == "" || *rdir == "" {
		fmt.Fprintln(stderr, "adamap-bench reduce: --manifest and --records are required")
		return 2
	}
	m, err := manifest.Load(*mpath)
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	recs, err := record.ReadDir(*rdir)
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	rep, err := reduce.Reduce(m, recs)
	if err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	rep.ManifestPath, rep.RecordsDir = filepath.ToSlash(*mpath), filepath.ToSlash(*rdir)
	md := rep.Markdown()
	if *out == "" {
		fmt.Fprint(stdout, md)
		return 0
	}
	// RESULTS.md holds measurements only: a synthetic report is never written under that name.
	if rep.Synthetic && strings.EqualFold(filepath.Base(*out), "RESULTS.md") {
		fmt.Fprintln(stderr, "adamap-bench: refusing to write a synthetic report to RESULTS.md: synthetic fixtures never become measurements")
		return 1
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		fmt.Fprintf(stderr, "adamap-bench: %v\n", err)
		return 1
	}
	return 0
}

// fakeAgent is the fake CLI that synthetic manifests run (runner.FakeAgent): never a model.
func fakeAgent(in io.Reader, out io.Writer, environ []string) int {
	get := func(name string) int {
		for _, kv := range environ {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				n, _ := strconv.Atoi(v)
				return n
			}
		}
		return 0
	}
	return runner.FakeAgent(in, out, get("ADAMAP_BENCH_ATTEMPT"), get("ADAMAP_BENCH_FAKE_FAIL"))
}
