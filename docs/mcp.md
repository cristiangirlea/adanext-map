# adamap mcp

`adamap mcp` serves the map and the queries as MCP tools, so an agent host (Claude Code, Codex,
hrn, any MCP client) can call them instead of grepping and opening files. It speaks MCP over stdio
only: the host starts it and talks on its stdin and stdout. There is no network listener.

```text
adamap mcp     [--repo dir] [--commit sha]
adamap install [--print | --claude | --codex] [--repo dir]
```

## adamap mcp

| Flag | Default | Meaning |
|---|---|---|
| `--repo` | `.` | The repository root, whose `.adamap` directory holds the store. |
| `--commit` | none | Answer from this commit's store only (a full hash). |

Stdout carries only the protocol. One line goes to stderr when the server starts, and another when
there is no store yet. The server stops with exit 0 when the host closes stdin. It exits 1 when
`--repo` is not a directory and 2 for a usage error.

**Which store.** The server loads the store when it starts: `--commit`'s, or the one
`.adamap/latest` names. Without `--commit`, every call compares `latest`'s modification time and
size with what they were at the last load, and loads again when they differ. So after
`adamap index`, the next call answers from the new store, with no restart. With `--commit`, the
server stays on that store. A server started before there is any store answers each call with a
tool error saying to run `adamap index`, and picks the store up once it exists.

## The tools

Every tool is read-only and marked so (`readOnlyHint`), and none reaches outside the store and the
repository. A tool answers with two things:

- **Text content.** The compact text the CLI prints for the same question, one line per item (see
  [queries.md](queries.md) and [map.md](map.md)). The remarks the CLI writes to stderr, such as an
  empty answer or how many matches were left out, come as a last line in parentheses.
- **Structured content.** The CLI's `--json` answer, with `commit` (and `dirty`) first. Each tool
  declares it as an output schema.

| Tool | Inputs | Answers |
|---|---|---|
| `map` | `tokens` (default 1024), `for` | The ranked map within `tokens`, as `adamap map --tokens n --for words`. |
| `outline` | `file` | The symbols `file` defines, as `adamap outline`. |
| `find_symbol` | `name`, `limit` (default 25) | Symbols whose name fits, best first, as `adamap find --limit n`. |
| `refs` | `symbol` | Every occurrence of the symbol, as `adamap refs`. |
| `callers` | `symbol` | The definitions that reference the symbol, as `adamap callers`. |
| `impact` | `target`, `depth` (default 3) | What depends on a symbol or a file, as `adamap impact --depth n`. |
| `status` | none | The store's commit and size, the repository's HEAD, and whether the store is stale. |

Inputs are as on the command line. `file` is a path relative to the repository root (an absolute
path inside the repository works too). `symbol` is a display name that fits exactly one symbol, or a
SCIP symbol string. `target` is a file path in the index, or a symbol given either way. A tool's
description, which the host shows the model, says what it returns and when to call it instead of
reading files or grepping.

### status

`status` answers even when there is no store. Its text looks like this:

```text
store: 0123456789ab, indexed 2026-10-13T09:00:00Z by scip-go v0.2.7: 3 documents, 9 symbols, 20 occurrences
HEAD: fedcba987654, no uncommitted changes
stale: the store describes 0123456789ab, not HEAD
refresh: adamap index --repo /work/project
```

A store is stale when it does not describe the working tree: it is for a commit other than HEAD,
tracked files have uncommitted changes, or it was indexed from a dirty tree. When git cannot be
asked, `status` says it cannot tell. Structured content:

| Field | Meaning |
|---|---|
| `repo` | The repository root, absolute. |
| `store`, `store_error` | Whether a store loaded, and why not. |
| `commit`, `store_dirty` | The commit the store describes, and whether it was indexed with uncommitted changes. |
| `pinned` | `true` when `--commit` fixed the store. |
| `indexer`, `created`, `counts` | From the store's header ([index.md](index.md)). |
| `head`, `dirty`, `at_head`, `git_error` | The repository's HEAD, whether tracked files have changes now, whether the store is for HEAD, and why git could not tell. |
| `stale`, `refresh` | Whether the store is stale, and the command that rebuilds it. |

### Errors

A call that cannot be answered gets a tool error (`isError`), not a protocol error, with a message
the model can act on. Examples: no store (run `adamap index`), an unknown file or symbol
(find_symbol lists the names), a name that fits several symbols (the candidates are listed with
their symbol strings, so the model can pass one), a negative `tokens`, `limit` or `depth`.

### A hostile store

The store comes from an indexer's output of the repository, so its names and paths may hold
terminal escapes. Text content escapes them as the CLI does: a path, name or kind holding a
control or format character is printed in Go quotes, and the map blanks such characters in
details. Every line of a result or an error is also checked, and quoted when it still holds one.
Structured content is JSON and keeps the raw values, as `--json` does.

## adamap install

`adamap install` gives a host the entry that starts the server for this repository. The entry runs
this adamap by its absolute path (`os.Executable`), with `mcp --repo <absolute repo>`. Install
adamap where it will stay (`go install`, or a release binary) before you run install: a `go run`
build lives in a temporary directory.

| Flag | Does |
|---|---|
| `--print` (the default) | Prints the entry for Claude Code and hrn (`.mcp.json`) and for Codex (TOML). Writes nothing. |
| `--claude` | Writes the `adamap` entry into the repository's `.mcp.json`, creating the file if needed. |
| `--codex` | Prints the block for `~/.codex/config.toml`, and nothing else, so it can be appended. Writes nothing. |

`--repo` (default `.`) names the repository: what the server answers about, and where `.mcp.json`
goes. Install never writes outside it.

### Claude Code

```text
adamap install --claude
```

This writes or updates `.mcp.json` at the repository root, Claude Code's project MCP file:

```json
{
  "mcpServers": {
    "adamap": {
      "type": "stdio",
      "command": "/home/me/go/bin/adamap",
      "args": [
        "mcp",
        "--repo",
        "/work/project"
      ]
    }
  }
}
```

Other servers in the file stay as they are, in their order. An existing `adamap` entry gets a new
`type`, `command` and `args`, and keeps its other fields (an `env`, say). The file is rewritten
with two-space indentation, as Claude Code writes it, and a second install leaves it unchanged.
Install refuses, and leaves the file alone, when `.mcp.json` is not valid JSON, is not an object,
has an `mcpServers` or `adamap` that is not an object, or is a link rather than a plain file.
Claude Code asks you to approve a project server the first time it starts in the project. The
absolute paths are particular to your machine, so think twice before committing the file.

### Codex

```text
adamap install --codex >> ~/.codex/config.toml
```

prints:

```toml
[mcp_servers.adamap]
command = "/home/me/go/bin/adamap"
args = ["mcp", "--repo", "/work/project"]
```

Install does not touch `~/.codex/config.toml` itself: it is outside the repository. Append the
block yourself, or edit an existing `[mcp_servers.adamap]` block.

### hrn

hrn reads Claude Code's format, so the `.mcp.json` that `--claude` writes works as it is:

```text
adamap index
adamap install --claude
hrn run --mcp .mcp.json -b claude "where is the store loaded, and who calls it?"
```

hrn offers the tools to the model as `mcp__adamap__map`, `mcp__adamap__refs` and so on. hrn
approves tools marked `readOnlyHint` by itself, and every adamap tool is marked so.
