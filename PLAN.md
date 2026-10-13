# adamap plan

The work queue and the decisions log. An agent session reads this file, takes the first unchecked
slice whose prerequisites are done, delivers it as one pull request under AGENTS.md's rules, and
updates this file in that pull request. Tracking issue: #7.

## Slices

- [x] 1. scaffold: AI setup, CI, README, PLAN and licence (#1)
  Done: PR #9, 2026-10-13.
- [x] 2. scip: read an index and store it per commit (#2)
  Done: PR #23, 2026-10-13.
- [x] 3. query: outline, find, refs, callers and impact (#3)
  Done: PR #?, 2026-10-13.
- [x] 4. map: a ranked repo map within a token budget (#4)
  Done: PR #?, 2026-10-13.
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
- 2026-10-13 (slice 3): a symbol is addressed by its SCIP symbol string, or by a display name that fits
  exactly one symbol (equal, else equal ignoring case; a prefix or fragment is `find`'s job), and an
  ambiguous name is refused with the candidates listed. Local symbols (`local N`) cannot be addressed,
  since the same ID is another symbol in each document, so no query lists them. A symbol only named by
  occurrences gets its name from its symbol string. `callers` places a reference in the innermost definition
  whose enclosing range (on the definition occurrence, not on the reference) contains it; without one, the
  span to the next global definition, clipped to the scope around it (docs/queries.md). `impact` lists each
  symbol once at its shallowest depth, never the target, default depth 3, and does not cap the list.
  Answers are one JSON object per line with `commit` first; lines are one-based; the standard library only.
- 2026-10-13 (slice 4): the map's edge from file A to file B adds, per symbol of B that A uses, the
  square root of that symbol's reference count, so the damping works per symbol as its reason
  asks. Dangling files spread their rank uniformly even when `--for` personalises the jump, as the
  slice asked; the jump is then 15% of the rank, so a matched file rises without always leading.
  The budget is filled by symbol score across files (Aider's way), not file by file, and the chosen
  symbols are printed under their files in rank order: walking files strictly in order lets one
  large file take the whole budget. Filling stops at the first symbol that does not fit. Local
  symbols, namespaces and parameters are not shown and add no edges. `internal/rank` is the map's
  own package and does not use `internal/query`.
