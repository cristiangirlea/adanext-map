# adamap, a code map for AI agents

One Go binary, `adamap`, that turns a SCIP index (rust-analyzer, scip-go, scip-typescript) into a
per-commit store under `.adamap/` and answers from it: `outline`, `find`, `refs`, `callers`,
`impact`, and a ranked repo map within a token budget, over a CLI and an MCP server. No running
language server, no C compiler, no model calls. It is one of the `adanext-*` tools around Ada,
hrn watch's bot (the harness repository).

Read the README for what it is and is not. `PLAN.md` is the work queue and the decisions log: read
it before taking an item, and don't reopen a recorded decision. `docs/` holds the references as
they are written (`queries.md`, `map.md`, `mcp.md`).

## Layout

| Path | Holds |
|---|---|
| `cmd/adamap/` | The CLI: `run()` returns the exit code so tests call it; one file per command. |
| `internal/scip/` | Reads a SCIP index into adamap's model: documents, symbols, occurrences, relationships. |
| `internal/store/` | The per-commit store under `.adamap/<commit>/`: write, load, header. |
| `internal/query/` | outline, find, refs, callers, impact over a loaded store. |
| `internal/rank/` | The reference graph and PageRank behind the repo map; the budgeted renderer. |
| `internal/mcpserver/` | `adamap mcp`: the queries as MCP tools over stdio. |
| `bench/` | The benchmark: tasks, the runner, `RESULTS.md`. |

A package that does not exist yet arrives with its slice in `PLAN.md`.

## Build and test

Go as pinned in `go.mod`. What CI (`.github/workflows/ci.yml`) runs, in order; run the same before
a PR (`/check` does):

```bash
go vet ./...
go build ./...
go test -count=1 ./...
go test -race -count=1 ./...     # CI only; needs cgo
test -z "$(gofmt -l .)"          # /check: tracked files only
bash -n .claude/hooks/go-rules.sh && bash -n .claude/hooks/go-rules.test.sh
sh .claude/hooks/go-rules.test.sh
```

`-race` needs cgo, which the owner's Windows PC lacks: run `go test -count=3 ./...` there and leave
`-race` to CI. CI runs on the owner's lab Linux runner (`vars.CI_RUNNER`) for pushes and the
repository's own pull requests; a fork's pull request runs on GitHub's hosted runner, never on the
lab. No Windows job on pull requests.

## Rules of this repository

- **Few dependencies, all named.** The SCIP Go bindings (`github.com/sourcegraph/scip`) and the
  official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) with what they need; otherwise
  the standard library. Another one is a decision recorded in `PLAN.md` first.
- **No model calls.** adamap never calls a model API, and tests never need a key or the network.
  The benchmark runs agent CLIs on their plans and refuses to run when an API key is set.
- **The store stays loadable.** Fields added to the store's types are additive with `omitempty`; a
  store written by an earlier version still loads, or the loader says it is too old and how to
  rebuild it.
- **Indexers are external programs** found at run time, never dependencies. When one is missing,
  `adamap index` names it and how to install it.
- **Tests on fixtures.** A SCIP index built in code, never a checked-in binary; queries and the
  MCP server tested over in-memory pipes.
- A release tag must match `Version` in `cmd/adamap/main.go`; tags and releases are the owner's.
- Every PR updates the relevant guide in `docs/` (and README when the overview changes) for a new
  flag, command or output, and `PLAN.md` for the item it delivers (tick the box, a `Done: PR #n,
  <date>` line, deviations in the Decisions log).

## Working rules

- **A branch delivers what it is for, nothing else.** One purpose per branch (one issue or PLAN.md
  item; short name, as in `scip-reader`) and one PR, whose description lists the files it touches
  and why. Larger work is a feature branch: each piece is a branch whose PR targets the feature
  branch under every condition below, and the feature branch reaches `main` through one PR
  reviewed for how the pieces fit. A problem found on the way is fixed here only if this branch
  caused it or it stops the change meeting its acceptance criteria; otherwise it gets an issue and
  a branch of its own. No refactors, renames or extras the item did not ask for.
- **Everything is tested before a merge**: the full local checks (`/check`), tests for every
  behaviour the change adds or alters, and CI green. Only then may an agent merge its PR, and only
  through the PR (`gh pr merge <n> --merge --delete-branch --match-head-commit <sha>`), when an
  independent review ran and its blocking findings are fixed, a security read of the diff has
  nothing open, and the head being merged is the commit all of that ran on. Merge `main` in only
  for a conflict, or once before those final checks and review. What could not be tested is said
  in the PR and waits for the owner. Never auto-merge (`--auto`) or `--admin`, never push to
  `main`, never tag or release: hand over the command.
- Commit messages: the area first, lower case, then a plain sentence (`scip: read enclosing ranges
  as the callers' scopes`), one logical change per commit. No emoji, no AI attribution anywhere
  (no `Co-Authored-By`, `Assisted-by` or "Generated with", in commits, PR text, comments or
  docs). Commits are `Cristian Girlea <cristiangirlea@gmail.com>`: check `git config user.email`
  first.
- Self-review with `/code-review`. A finding is blocking when it is a defect this branch brings in,
  a missing test for what it changes, a broken rule of this file, or a security hole: fix each as
  its own commit, with the smallest change that closes it, and don't squash. Everything else
  (problems already on `main`, improvements, style) becomes an issue linked from the PR.
- Tests first, seen failing. On Windows the temp dir has 8.3 short names: compare paths after
  `filepath.EvalSymlinks`. Files are LF (`.gitattributes`).
- Secrets are never read, printed or committed. Small diffs: don't restyle what you didn't change.
  Not sure? Ask, with one proposal.
