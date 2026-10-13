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
  Done: PR #31, 2026-10-13.
- [x] 4. map: a ranked repo map within a token budget (#4)
  Done: PR #32, 2026-10-13.
- [x] 5. mcp: serve the queries to agents (#5)
  Done: PR #33, 2026-10-13.
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
  `index.scip` already in the repository rather than overwrite and delete it (replaced in issue #12:
  the indexer now writes into a temporary directory outside the repository, so there is nothing to
  refuse and a run stopped by a signal leaves nothing behind in the repository). The converter reads
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
  and in a kept document a malformed range or a symbol without a symbol, stay fatal; a path that
  cleans to the project root (`pkg/..`) is skipped too. `store.Load` still refuses such a
  path, since a store adamap wrote never holds one.
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
- 2026-10-13 (slice 5): the MCP Go SDK is `github.com/modelcontextprotocol/go-sdk` v1.8.0; it brings
  `google/jsonschema-go`, `segmentio/encoding` and `segmentio/asm`, `yosida95/uritemplate`,
  `golang.org/x/oauth2`, `x/time`, `x/sync` and `x/sys`. `internal/mcpserver` takes the CLI's path rule and git as two functions in
  its `Config`, so it neither copies nor moves the helpers in `cmd/adamap`. The tool for `find` is
  named `find_symbol`, as the slice asked. A tool answers with the CLI's text, its stderr remarks as
  a last line in parentheses, and the CLI's JSON as structured content; the map's JSON type is
  declared again in `mcpserver`, since `cmd/adamap`'s cannot be imported. The lists of refs, callers
  and impact are not capped, as on the command line. The store is loaded at start and loaded again
  when `.adamap/latest`'s modification time or size changes (checked at each call); `--commit` pins
  it. `status` answers without a store, since saying there is none is its job; the other tools give
  a tool error. `status` does not call a store indexed from a dirty tree at HEAD stale while the
  tree is still dirty, since indexing again could not clear that; it says it cannot tell. Every line of a text result or error passes through `query.Safe` after the
  per-field escaping. All tools are marked read-only, which hrn approves by itself. `adamap
  install --claude` writes `"type": "stdio"`, `command` and `args` into the `adamap` entry of
  `.mcp.json` and keeps the entry's other fields and every other key in order; of a key written
  twice it reads the last, as JSON readers do. It refuses a link and a file it cannot parse. `--codex` only prints: `~/.codex/config.toml` is outside the
  repository.
- 2026-10-13: item-count limits for an index (issue #17), checked while the store is decoded and
  after a SCIP index is read and converted: 2,000,000 documents, 20,000,000 symbols (global and
  local), 50,000,000 occurrences and 20,000,000 relationships. Bytes do not bound memory, since a
  few bytes (`{},`) become a whole struct. The defaults sit far above the measured Rust workspace
  (2,000 files, about 2.4 million occurrences) so no real repository meets them, and stay low
  enough that a planted store or index is refused before it exhausts memory.
- 2026-10-13 (#8): the push guard is a PreToolUse hook on Bash and PowerShell,
  `.claude/hooks/push-guard.sh`, in POSIX sh with git, sed and tr only. settings.json's deny rules
  stay as a first layer. It asks git where a push without a destination goes (`@{push}`,
  `push.default`, `remote.*.push`), refuses what it cannot see (a variable, a command substitution,
  xargs, an encoded PowerShell command), and fails closed only for input that mentions a push, a
  tag, a release, a merge or the API, so a fault in the hook never blocks other commands. Scripts
  run by name (`sh x.sh`, `-File x.ps1`) are not read.
- 2026-10-13 (slice 6, the framework only): the benchmark lives under `bench/`: the command
  `bench/cmd/adamap-bench` and its packages in `bench/internal/` (manifest, record, reduce, runner),
  standard library only. The manifest is strict JSON; run IDs are derived from the cell
  (`<task>.<condition>.<backend>.r<n>`) and IDs are unique across conditions, tasks and backends, so
  a swapped record names no planned run. The conditions are counterbalanced by a seeded Latin
  square over blocks of one task, repeat and backend. Records carry no success flag: the reducer
  recomputes it from check outputs whose hashes it verifies. Usage keeps each provider's semantics
  (Anthropic input without the cache; Codex and Gemini input with it), and missing usage stays
  missing. A real run needs `--i-have-approval-for-N-runs` equal to the plan's run count and no
  provider key variable set; the fake backend runs only in a synthetic manifest, whose outputs say
  SYNTHETIC and never go to `RESULTS.md`. The adoption rule's latency bound is `max_latency_ratio`
  in the manifest (1.25 in the example, to confirm before any run). The example manifest leaves
  every identity `UNPINNED` and its 16 tasks `candidate`, so it cannot run until the owner pins and
  approves them. Slice 6 stays open for the authorized pilot. Issue #6's text was read from its
  public page, since `gh` had no valid credentials in this session; the candidate tasks were taken
  from merge commits in git history, for the same reason.
- 2026-10-13 (#24, query follow-ups): `impact` reports `unattributed` as `callers` does: the references,
  to the target or to a symbol whose callers the walk looked up, that lie in no definition (a walk
  that reaches one now prints a note on stderr). `--json` writes DEL and the C1 controls as unicode
  escapes; the MCP structured content stays the SDK's encoding. A fallback scope starts where an
  enclosing range ends on its name's line, not inside that range. `scopeAt` searches a chain of
  scopes with strictly growing ends (skip pointers) instead of walking every nesting level.
  `impact` given an absolute path outside the repository that is no symbol either answers with the
  path error. `Safe` relies on `strconv.IsPrint` alone, which already refuses format characters.
- 2026-10-13 (#67, the pilot's preparation): a run that meets the plan's usage limit, or a
  rate-limit or overload error the CLI stopped retrying, stops the execution with no record and
  no retry. It leaves `<run id>.limited` instead of the `.started` marker, so resume runs it again.
  Claude Code documents only the `api_retry` event's error categories, not a usage-limit shape in
  stream-json. So the limit is read conservatively, from error results, those categories and a
  failed attempt's stderr, never from a successful answer. `pilot-1.json` and
  `pilot-1.tasks.json` are the pilot: the 16 candidates approved by the owner, identities pinned as
  installed here, 240 runs. The example's `codegraph index .` fails on a fresh tree, so CodeGraph
  is prepared with `init -y` and served with `serve --mcp`. It is started through node and its
  package's shim, since the npm wrapper is a `.cmd` file off PATH. The LSP condition gets gopls
  from Anthropic's `gopls-lsp` plugin, enabled through each condition's `enabledPlugins` setting.
  The owner installs that plugin and adamap at the pinned commit before the wiring smoke.
  `max_latency_ratio` stays 1.25.
