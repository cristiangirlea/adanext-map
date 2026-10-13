# adamap plan

The work queue and the decisions log. An agent session reads this file, takes the first unchecked
slice whose prerequisites are done, delivers it as one pull request under AGENTS.md's rules, and
updates this file in that pull request. Tracking issue: #7.

## Slices

- [x] 1. scaffold: AI setup, CI, README, PLAN and licence (#1)
  Done: PR #9, 2026-10-13.
- [x] 2. scip: read an index and store it per commit (#2)
  Done: PR #23, 2026-10-13.
- [ ] 3. query: outline, find, refs, callers and impact (#3)
- [ ] 4. map: a ranked repo map within a token budget (#4)
- [ ] 5. mcp: serve the queries to agents (#5)
- [ ] 6. bench: measure against no tool, LSP, Serena and CodeGraph (#6)

Later, each as its own issue when its turn comes: scip-typescript; re-indexing on commit (a git
hook or `adamap watch`); hrn passing the server to every backend (an issue in the harness
repository); a fail-closed push guard (#8).

## Decisions

- 2026-10-13: public from the first commit. Go, for one static binary that reads SCIP natively and
  builds in seconds. The name scheme `adanext-<what it is>` for this family of tools; the binary is
  `adamap`.
- 2026-10-13: CI on GitHub's standard hosted runners (Linux and Windows), free for a public
  repository, never the owner's lab: outside contributions stay off their machines, as in harness.
- 2026-10-13: dependencies are the SCIP Go bindings and the official MCP Go SDK, otherwise the
  standard library. Indexers are external programs.
- 2026-10-13: the store is keyed by commit, so a question can be asked of a specific commit and
  two commits compared.
- 2026-10-13: no claim before the benchmark: the README quotes `bench/RESULTS.md`, nothing else.
- 2026-10-13 (slice 2): the SCIP bindings are `github.com/scip-code/scip/bindings/go/scip` v0.10.0,
  where the project moved from sourcegraph/scip; they bring `google.golang.org/protobuf` and
  `github.com/sourcegraph/beaut`. The model is its own package, `internal/model`, so the store does
  not depend on SCIP's types. The store is gzip-compressed JSON from the standard library, format 1.
- 2026-10-13 (slice 2): `--commit` takes only a full hash, since it names a directory; with it, git
  is not asked and the store is not marked dirty. `adamap index` refuses to run an indexer over an
  `index.scip` already in the repository rather than overwrite and delete it. The converter reads
  the typed ranges SCIP added as well as the packed ones, merges a document listed twice, takes a
  symbol's first definition occurrence as its definition, and counts a forward definition as a
  reference.
- 2026-10-13 (slice 2): a backslash in a document path is always a separator: SCIP specifies `/`
  and indexers on Windows (scip-go 0.2.7) write `\`. The converter turns it into `/` and cleans the
  path (`a/./b.go` and `a//b.go` become `a/b.go`) before checking it, and stores the slash form.
- 2026-10-13 (slice 2): a document whose path leaves the project (absolute, drive-letter, UNC, or
  `..` escaping, after normalising) is skipped with its occurrences and symbols instead of failing
  the index: scip-go on Windows also indexes the test main Go generates in its build cache, outside
  the repository. `adamap index` says how many on stderr and still writes the store. An empty path,
  a malformed range or a symbol without a symbol stay fatal, and `store.Load` still refuses such a
  path, since a store adamap wrote never holds one.
