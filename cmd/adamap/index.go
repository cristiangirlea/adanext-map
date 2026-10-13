package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// indexer is an external program that writes index.scip in its working directory, chosen by the
// project file at the repository root.
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
		fmt.Fprintf(stderr, "adamap index: skipped %d documents outside the project (first: %s)\n",
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

// resolveCommit is the given commit, or git's HEAD in repo and whether tracked files have changes
// since it. A given commit is taken as the caller says, and git is not asked.
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
	status, err := gitOutput(repo, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", false, fmt.Errorf("cannot tell whether %s has changes: %w", repo, err)
	}
	return head, status != "", nil
}

// gitSafely are the options in front of every git command adamap runs. The repository's own
// .git/config and .git/hooks are written by whoever made it, and git would otherwise run what they
// name: core.fsmonitor on every status, and the post-index-change hook whenever status rewrites the
// index. -c overrides the repository's config, and reaches the git that status starts in a
// submodule. Clean filters are not covered: status runs one to compare a file whose timestamp
// changed, and no option turns off a filter driver the repository defines (docs/index.md).
var gitSafely = []string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}

// gitOutput is the output of `git args` in repo, run with gitSafely. Every git call goes through it.
func gitOutput(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", slices.Concat([]string{"-C", repo}, gitSafely, args)...)
	var errOut strings.Builder
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runAndRead runs the indexer repo needs, with repo as its working directory, then reads the
// index.scip it wrote there and deletes it. An index.scip already there is not adamap's to replace.
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
	out := filepath.Join(repo, "index.scip")
	if _, err := os.Lstat(out); !errors.Is(err, fs.ErrNotExist) {
		return nil, scip.Skipped{}, fmt.Errorf("%s already exists: pass it with --scip, or move it so %s can write its own", out, ix.program)
	}
	defer os.Remove(out)
	if err := runIndexer(repo, program, ix.args, stderr); err != nil {
		return nil, scip.Skipped{}, fmt.Errorf("%s failed: %w", ix.program, err)
	}
	return scip.ReadFile(out, maxBytes)
}
