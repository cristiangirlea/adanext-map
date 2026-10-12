# adamap — a code map for AI agents

`adamap` gives coding agents exact code navigation without a running language server. It reads
the index a compiler-grade tool already produces (SCIP, from rust-analyzer, scip-go or
scip-typescript), keeps it per commit under `.adamap/`, and answers the questions agents ask most
in milliseconds, as a CLI and as an MCP server:

- `adamap map --tokens 2000` — the files and symbols that matter most, small enough to open a task with
- `adamap outline <file>` — what a file defines
- `adamap find <name>`, `adamap refs <symbol>`, `adamap callers <symbol>`, `adamap impact <symbol>`

One Go binary. No C compiler, no language server process, no model calls, no telemetry.

**Status: being built.** Nothing to install yet; [PLAN.md](PLAN.md) lists the slices in the order
they land.

## Why another one

Tools in this space either keep a language server running per language, parse the code
approximately, or publish only their own numbers. adamap takes the compiler's own index, built
once per commit, and ships its benchmark: `bench/RESULTS.md` will hold tokens, time and
correctness against no tool, the host's built-in LSP, Serena and CodeGraph, reproducible from this
repository. Until those numbers exist, this README makes no claim.

## How it will work

1. `adamap index` runs the indexer that fits the repository (or reads a given `index.scip`) and
   writes the store for the current commit.
2. The queries and the map read the store; nothing is parsed at question time.
3. `adamap mcp` serves the same queries as MCP tools; `adamap install` writes the host's entry
   (Claude Code, Codex, any MCP host).

## The family

adamap is one of the `adanext-*` tools around Ada, the bot of
[hrn](https://github.com/cristiangirlea/harness): hrn will hand the map to every agent backend it
runs.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) and [SECURITY.md](SECURITY.md).
MIT licence.
