# adamap plan

The work queue and the decisions log. An agent session reads this file, takes the first unchecked
slice whose prerequisites are done, delivers it as one pull request under AGENTS.md's rules, and
updates this file in that pull request. Tracking issue: #7.

## Slices

- [ ] 1. scaffold: AI setup, CI, README, PLAN and licence (#1)
- [ ] 2. scip: read an index and store it per commit (#2)
- [ ] 3. query: outline, find, refs, callers and impact (#3)
- [ ] 4. map: a ranked repo map within a token budget (#4)
- [ ] 5. mcp: serve the queries to agents (#5)
- [ ] 6. bench: measure against no tool, LSP, Serena and CodeGraph (#6)

Later, each as its own issue when its turn comes: scip-typescript; re-indexing on commit (a git
hook or `adamap watch`); hrn passing the server to every backend (an issue in the harness
repository); a lab CI runner for this repository (infra#59).

## Decisions

- 2026-10-13: public from the first commit. Go, for one static binary that reads SCIP natively and
  builds in seconds on the lab runner. The name scheme `adanext-<what it is>` for this family of
  tools; the binary is `adamap`.
- 2026-10-13: dependencies are the SCIP Go bindings and the official MCP Go SDK, otherwise the
  standard library. Indexers are external programs.
- 2026-10-13: the store is keyed by commit, so a question can be asked of a specific commit and
  two commits compared.
- 2026-10-13: no claim before the benchmark: the README quotes `bench/RESULTS.md`, nothing else.
