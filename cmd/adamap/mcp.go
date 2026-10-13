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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cristiangirlea/adanext-map/internal/mcpserver"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

func mcpUsage(w io.Writer, fl *flag.FlagSet) {
	fmt.Fprint(w, `usage: adamap mcp [--repo dir] [--commit sha]

Serves the map and the queries as MCP tools on stdin and stdout, for an agent host to start
(adamap install writes its entry). Stdout carries only the protocol; notes go to stderr. It
answers from the store indexed last and follows .adamap/latest, or from --commit's store only.

`)
	fl.SetOutput(w)
	fl.PrintDefaults()
}

// runMCP is `adamap mcp`: it serves until the client closes stdin, then exits 0. It exits 1 when
// --repo is not a directory or the session fails, and 2 for a usage error.
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	repo := fl.String("repo", ".", "the repository root, whose .adamap directory holds the store")
	commit := fl.String("commit", "", "answer from this commit's store only (default: the store indexed last, followed as it changes)")
	usageError := func(err error) int {
		fmt.Fprintf(stderr, "adamap mcp: %v\n\n", err)
		mcpUsage(stderr, fl)
		return 2
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			mcpUsage(stdout, fl)
			return 0
		}
		return usageError(err)
	}
	switch {
	case fl.NArg() > 0:
		return usageError(fmt.Errorf("unexpected argument %q", fl.Arg(0)))
	case *commit != "":
		if err := store.CheckCommit(*commit); err != nil {
			return usageError(fmt.Errorf("--commit: %w", err))
		}
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "adamap mcp: %v\n", err)
		return 1
	}
	root, err := filepath.Abs(*repo)
	if err != nil {
		return fail(err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fail(fmt.Errorf("--repo %s is not a directory", root))
	}

	srv := mcpserver.New(mcpserver.Config{
		Repo:    root,
		Commit:  *commit,
		Version: Version,
		Path:    func(arg string) (string, error) { return indexPath(root, arg) },
		Head:    func() (string, bool, error) { return resolveCommit(root, "") },
	})
	fmt.Fprintf(stderr, "adamap mcp: serving %s over stdio\n", root)
	if *commit == "" {
		if _, err := store.Latest(root); err != nil {
			fmt.Fprintf(stderr, "adamap mcp: %v; the tools say so until a store exists\n", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = srv.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(stdin), Writer: nopWriteCloser{stdout}})
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, mcp.ErrConnectionClosed) && ctx.Err() == nil {
		return fail(err)
	}
	return 0
}

// nopWriteCloser is a writer the transport may close without closing the writer under it: run's
// stdout belongs to its caller.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
