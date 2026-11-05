package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/query"
	"github.com/cristiangirlea/adanext-map/internal/scip"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// indexer is an external program chosen by the project file at the repository root. It runs in the
// repository and writes its index where --output says, which is never inside the repository.
type indexer struct {
	marker  string
	program string
	args    []string
	install string
}

// indexers are tried in order: the first whose marker exists is run.
var indexers = []indexer{
	{marker: "Cargo.toml", program: "rust-analyzer", args: []string{"scip", "."}, install: "rustup component add rust-analyzer"},
	{marker: "go.mod", program: "scip-go", install: "go install github.com/scip-code/scip-go/cmd/scip-go@latest"},
}

// lookPath and runIndexer are the seams tests replace, so no test needs a real indexer.
var lookPath = exec.LookPath

var runIndexer = func(dir, program string, args []string, stderr io.Writer) error {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	// The indexer's progress goes to stderr, keeping stdout for adamap's summary.
	cmd.Stdout, cmd.Stderr = stderr, stderr
	return cmd.Run()
}

func indexUsage(w io.Writer, fl *flag.FlagSet) {
	fmt.Fprint(w, `usage: adamap index [--scip path] [--commit sha] [--repo dir] [--max-bytes n]

Reads a SCIP index into the store under <repo>/.adamap/<commit>/. Without --scip it runs the
indexer the repository needs: rust-analyzer for Cargo.toml, scip-go for go.mod.

`)
	fl.SetOutput(w)
	fl.PrintDefaults()
}

// runIndex is `adamap index`: exit 0 on success, 1 when indexing fails, 2 for a usage error.
func runIndex(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("index", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	scipPath := fl.String("scip", "", "read this SCIP index instead of running an indexer")
	commit := fl.String("commit", "", "the full hash of the commit the index describes (default: git's HEAD)")
	repo := fl.String("repo", ".", "the repository root; the store goes in its .adamap directory")
	maxBytes := fl.Int64("max-bytes", scip.DefaultMaxBytes, "refuse an index larger than this many bytes")
	usageError := func(err error) int {
		fmt.Fprintf(stderr, "adamap index: %v\n\n", err)
		indexUsage(stderr, fl)
		return 2
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			indexUsage(stdout, fl)
			return 0
		}
		return usageError(err)
	}
	switch {
	case fl.NArg() > 0:
		return usageError(fmt.Errorf("unexpected argument %q", fl.Arg(0)))
	case *maxBytes <= 0:
		return usageError(fmt.Errorf("--max-bytes must be positive, not %d", *maxBytes))
	case *commit != "":
		if err := store.CheckCommit(*commit); err != nil {
			return usageError(fmt.Errorf("--commit: %w", err))
		}
	}

	start := time.Now()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "adamap index: %v\n", err)
		return 1
	}
	// The commit comes first, so a repository that has none fails before a slow indexer runs.
	c, dirty, err := resolveCommit(*repo, *commit)
	if err != nil {
		return fail(err)
	}
	var idx *model.Index
	var skipped scip.Skipped
	if *scipPath != "" {
		idx, skipped, err = scip.ReadFile(*scipPath, *maxBytes)
	} else {
		idx, skipped, err = runAndRead(*repo, *maxBytes, stderr)
	}
	if err != nil {
		return fail(err)
	}
	if skipped.Count > 0 {
		fmt.Fprintf(stderr, "adamap index: skipped %d documents adamap cannot store (outside the project or not a safe file name) (first: %s)\n",
			skipped.Count, strings.Join(skipped.Paths, ", "))
	}
	dir, err := store.Write(*repo, store.Header{
		Adamap:      Version,
		Commit:      c,
		Dirty:       dirty,
		Indexer:     idx.Tool,
		ProjectRoot: idx.ProjectRoot,
		Created:     time.Now().UTC(),
	}, idx)
	if err != nil {
		return fail(err)
	}
	mark := ""
	if dirty {
		mark = " (dirty)"
	}
	n := idx.Counts()
	fmt.Fprintf(stdout, "indexed %s%s: %d documents, %d symbols, %d occurrences in %s -> %s\n",
		c[:12], mark, n.Documents, n.Symbols, n.Occurrences, time.Since(start).Round(time.Millisecond), dir)
	return 0
}

// resolveCommit is the given commit, or git's HEAD in repo and whether files in repo have changes
// since it: tracked files, and untracked ones that .gitignore does not hide, which an indexer would
// compile as well. A given commit is taken as the caller says, and git is not asked.
func resolveCommit(repo, given string) (string, bool, error) {
	if given != "" {
		return given, false, nil
	}
	head, err := gitOutput(repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", false, fmt.Errorf("cannot read the commit of %s: %w; pass --commit with its full hash", repo, err)
	}
	if err := store.CheckCommit(head); err != nil {
		return "", false, err
	}
	off, err := filtersOff(repo)
	if err != nil {
		return "", false, fmt.Errorf("cannot tell whether %s has changes: %w; pass --commit with its full hash", repo, err)
	}
	// The pathspec keeps the check to repo when it is a subdirectory of the work tree, and leaves out
	// its own store, which a .gitignore of the owner's can list.
	status, err := gitExecute(repo, off, true, "status", "--porcelain", "-z", "--untracked-files=normal", "--ignore-submodules=dirty",
		"--", ".", ":(exclude)"+store.DirName)
	if err != nil {
		return "", false, fmt.Errorf("cannot tell whether %s has changes: %w", repo, err)
	}
	return head, status != "", nil
}

// gitSafely are the options in front of every git command adamap runs, with hooks an empty
// directory. The repository's own .git/config and .git/hooks are written by whoever made it, and git
// would otherwise run what they name: core.fsmonitor on every status, and the post-index-change hook
// whenever status rewrites the index. -c overrides the repository's config, and reaches the git that
// status starts in a submodule. An empty core.fsmonitor means off on every git version, where
// "false" is a hook program's name to an older one. core.hooksPath is a directory made for the
// call, as /dev/null is no path on Windows. Filter drivers are switched off by filtersOff, for the
// one command that runs them.
func gitSafely(hooks string) []string {
	return []string{"--no-optional-locks", "-c", "core.fsmonitor=", "-c", "core.hooksPath=" + filepath.ToSlash(hooks)}
}

// filtersOff are the -c options that switch off every filter driver configured for repo, in the
// system, global and repository config alike. git status runs a clean filter, or starts a process
// filter, to compare a file whose timestamp changed, and the driver is a program the config names.
// An empty command is no filter, and required=false keeps git from stopping at the filter it was
// told not to run. The cost is that a file a driver would change (git-lfs's pointers, say) can show
// as modified, so the tree can read as dirty (docs/index.md). A driver whose name has an = in it
// cannot be written as -c name.clean=, which git splits at the first =, so that is an error. A
// submodule's drivers are in its own config, which this does not list, and status would start a git
// inside it to read them: resolveCommit passes --ignore-submodules=dirty, so none is started.
func filtersOff(repo string) ([]string, error) {
	keys, err := gitOutput(repo, "config", "--name-only", "--get-regexp", `^filter\.`)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 { // no filter is configured
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, key := range strings.Split(keys, "\n") {
		rest := strings.TrimPrefix(key, "filter.")
		dot := strings.LastIndex(rest, ".")
		if dot < 1 { // filter.<name>.<variable>; a key with no name has no driver
			continue
		}
		name := rest[:dot]
		if strings.Contains(name, "=") {
			return nil, fmt.Errorf("the filter driver %q has an = in its name, so adamap cannot switch it off", name)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	var off []string
	for _, name := range names {
		for _, v := range []string{"clean", "smudge", "process"} {
			off = append(off, "-c", "filter."+name+"."+v+"=")
		}
		off = append(off, "-c", "filter."+name+".required=false")
	}
	return off, nil
}

// gitOutput is the output of `git args` in repo, run through gitExecute with gitSafely.
// The environment is part of the guard too: GIT_NO_LAZY_FETCH (git 2.44 or later) keeps a partial
// clone from fetching, and no -c option does that.
func gitOutput(repo string, args ...string) (string, error) {
	return gitRun(repo, nil, args...)
}

// gitRun is gitOutput with more -c options after gitSafely's, in front of the command.
func gitRun(repo string, opts []string, args ...string) (string, error) {
	return gitExecute(repo, opts, false, args...)
}

// gitTimeout bounds reads of ignore files that may be FIFOs.
var gitTimeout = 2 * time.Minute

// gitCommand lets tests run a bounded slow subprocess without a real slow repository.
var gitCommand = exec.CommandContext

// statusProbe cancels git after the first complete porcelain record without keeping its output.
type statusProbe struct {
	cancel context.CancelFunc
	found  bool
}

func (p *statusProbe) Write(b []byte) (int, error) {
	if bytes.IndexByte(b, 0) >= 0 {
		p.found = true
		p.cancel()
	}
	return len(b), nil
}

func gitExecute(repo string, opts []string, dirtyOnly bool, args ...string) (string, error) {
	hooks, err := os.MkdirTemp("", "adamap-nohooks-*")
	if err != nil {
		return "", fmt.Errorf("git %s: cannot make an empty hooks directory: %w", args[0], err)
	}
	defer os.RemoveAll(hooks)
	ctx, stop := gitInterruptContext(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := gitCommand(ctx, "git", slices.Concat([]string{"-C", repo}, gitSafely(hooks), opts, args)...)
	configureGitProcess(cmd)
	cmd.WaitDelay = time.Second
	cmd.Env = gitEnvironment()
	var errOut strings.Builder
	cmd.Stderr = &errOut
	var out strings.Builder
	probe := &statusProbe{cancel: cancel}
	if dirtyOnly {
		cmd.Stdout = probe
	} else {
		cmd.Stdout = &out
	}
	err = cmd.Run()
	if probe.found {
		return "dirty", nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s: timed out after %s", args[0], gitTimeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], query.SafeLines(msg))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(out.String()), nil
}

// runAndRead runs the indexer repo needs, with repo as its working directory, and reads the index
// it wrote into a temporary directory of its own, removed on return. Nothing is written in repo, so
// an index.scip already there is left alone and a run stopped by a signal leaves nothing behind in it.
func runAndRead(repo string, maxBytes int64, stderr io.Writer) (*model.Index, scip.Skipped, error) {
	var ix *indexer
	for i := range indexers {
		if _, err := os.Stat(filepath.Join(repo, indexers[i].marker)); err == nil {
			ix = &indexers[i]
			break
		}
	}
	if ix == nil {
		return nil, scip.Skipped{}, fmt.Errorf("%s has no Cargo.toml or go.mod, so adamap does not know its indexer: "+
			"run one yourself and pass its output with --scip", repo)
	}
	program, err := lookPath(ix.program)
	if err != nil {
		return nil, scip.Skipped{}, fmt.Errorf("%s needs %s (found %s), which is not on PATH: install it with `%s`",
			repo, ix.program, ix.marker, ix.install)
	}
	tmp, err := os.MkdirTemp("", "adamap-index-*")
	if err != nil {
		return nil, scip.Skipped{}, fmt.Errorf("cannot make a directory for %s to write its index in: %w", ix.program, err)
	}
	defer os.RemoveAll(tmp)
	// The indexer runs in repo, so a relative temporary directory must be resolved here.
	tmp, err = filepath.Abs(tmp)
	if err != nil {
		return nil, scip.Skipped{}, fmt.Errorf("cannot resolve the directory for %s to write its index in: %w", ix.program, err)
	}
	out := filepath.Join(tmp, "index.scip")
	if err := runIndexer(repo, program, append(slices.Clone(ix.args), "--output", out), stderr); err != nil {
		if ix.program == "rust-analyzer" {
			return nil, scip.Skipped{}, fmt.Errorf("%s failed: %w; if rust-analyzer is only rustup's shim, install it with: rustup component add rust-analyzer", ix.program, err)
		}
		return nil, scip.Skipped{}, fmt.Errorf("%s failed: %w", ix.program, err)
	}
	return scip.ReadFile(out, maxBytes)
}

// gitEnvironment keeps caller repository selection and config injection out of git.
func gitEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "GIT_") && strings.HasSuffix(key, "_PATHSPECS") ||
			strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		switch key {
		case "GIT_NO_LAZY_FETCH", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR",
			"GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM",
			"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GIT_NO_LAZY_FETCH=1") // a missing promisor object is an error, not a fetch
	return env
}
