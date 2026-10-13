// Command adamap is a code map for AI agents, built slice by slice (PLAN.md): it is to read SCIP
// indexes into a per-commit store and answer outline, find, refs, callers, impact and a ranked
// repo map over a CLI and an MCP server. So far it has version, help and index.
package main

import (
	"fmt"
	"io"
	"os"
)

// Version is what `adamap version` prints. A release tag must match it.
const Version = "0.0.1-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without the process: the arguments after the program name, the two output streams,
// and the exit code as the result, so tests call it directly.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "adamap %s\n", Version)
		return 0
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "adamap: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: adamap <command>

commands:
  version   print the version
  help      print this text
  index     read a SCIP index into the store for a commit (adamap index -h for its flags)

The commands outline, find, refs, callers, impact, map and mcp arrive slice by slice;
PLAN.md lists them in order.
`)
}
