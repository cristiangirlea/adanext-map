# adamap — a code map for AI agents

**Status: being built; nothing to install yet.** [PLAN.md](PLAN.md) lists the slices in the order
they land. The commands so far are `adamap version`, `adamap help`, `adamap index`, the five
queries `adamap outline`, `find`, `refs`, `callers` and `impact` ([docs/queries.md](docs/queries.md)),
`adamap map` ([docs/map.md](docs/map.md)), and `adamap mcp` and `adamap install`
([docs/mcp.md](docs/mcp.md)).

What adamap is for: coding agents need exact code navigation, and today that means keeping a
language server running per language, or parsing the code approximately. adamap will instead read
the index a compiler-grade tool already produces (SCIP, from rust-analyzer, scip-go or
scip-typescript), keep it per commit under `.adamap/`, and answer the questions agents ask most,
as a CLI and as an MCP server:

- `adamap map --tokens 2000` — the files and symbols that matter most, small enough to open a task with
- `adamap outline <file>` — what a file defines
- `adamap find <name>`, `adamap refs <symbol>`, `adamap callers <symbol>`, `adamap impact <symbol|file>`

One Go binary. No C compiler, no language server process, no model calls, no telemetry.

## Why another one

Tools in this space either keep a language server running per language, parse the code
approximately, or publish only their own numbers. adamap will take the compiler's own index, built
once per commit, and ship its benchmark: `bench/RESULTS.md` is to hold tokens, time and
correctness against no tool, the host's built-in LSP, Serena and CodeGraph, reproducible from this
repository. Until those numbers exist, this README makes no claim.

Benchmark status: the framework exists ([bench/README.md](bench/README.md): the manifest, the
runner behind an approval gate, the records and the reducer); no measured results exist yet.

## How it will work

1. `adamap index` runs the indexer that fits the repository (or reads a given `index.scip`) and
   writes the store for the current commit.
2. The queries and the map read the store; nothing is parsed at question time.
3. `adamap mcp` serves the same queries as MCP tools; `adamap install` writes the host's entry
   (Claude Code, Codex, any MCP host).

## Use it from an agent

`adamap mcp` serves the map and the queries as MCP tools over stdio: `map`, `outline`,
`find_symbol`, `refs`, `callers`, `impact` and `status`. `adamap install --claude` writes the entry
into the project's `.mcp.json` for Claude Code (and hrn's `--mcp`), and `adamap install --codex`
prints the block for Codex's `config.toml`. [docs/mcp.md](docs/mcp.md) describes the tools and the
setup.

## The family

adamap is one of the `adanext-*` tools around Ada, the bot of
[hrn](https://github.com/cristiangirlea/harness): hrn will hand the map to every agent backend it
runs.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) and [SECURITY.md](SECURITY.md).
MIT licence.
