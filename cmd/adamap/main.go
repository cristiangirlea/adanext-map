// Command adamap is a code map for AI agents, built slice by slice (PLAN.md): it reads SCIP
// indexes into a per-commit store and answers outline, find, refs, callers, impact and a ranked
// repo map from it; an MCP server is still to come. So far it has version, help, index, the five
// queries and map.
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
	case "outline":
		return runOutline(args[1:], stdout, stderr)
	case "find":
		return runFind(args[1:], stdout, stderr)
	case "refs":
		return runRefs(args[1:], stdout, stderr)
	case "callers":
		return runCallers(args[1:], stdout, stderr)
	case "impact":
		return runImpact(args[1:], stdout, stderr)
	case "map":
		return runMap(args[1:], stdout, stderr)
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
  outline   list the symbols a file defines
  find      list the symbols whose name fits
  refs      list every occurrence of a symbol
  callers   list the symbols that call or use a symbol
  impact    list what depends on a symbol or a file, transitively
  map       print the files and symbols that matter most, within a token budget (adamap map -h)

The queries and map read the store that "adamap index" wrote; each takes --repo, --commit and
--json (adamap <command> -h). The command mcp arrives in a later slice; PLAN.md lists it.
`)
}
