# adamap's benchmark

The framework for issue #6: does adamap improve correctness, repeated exploration, model usage and
latency against no tool, the host's built-in LSP, Serena and CodeGraph? It holds the experiment
manifest, a runner that drives coding CLIs on their subscription plans, the result records, and the
reducer that writes `bench/RESULTS.md`.

**Status: the framework exists; no measured results exist yet.** No real agent run has happened.
The pilot needs the owner to pin every identity in the manifest, approve the tasks, and approve the
run budget. Until then there is no `bench/RESULTS.md`, and the README makes no claim.

adamap itself never calls a model. The runner starts the CLIs the manifest names, refuses to start
when a provider API key is set, and never moves to API access. It runs without hrn.

## The workflow

```bash
# 1. Check a manifest, a tasks file, or records against their manifest.
go run ./bench/cmd/adamap-bench validate --manifest bench/manifest.example.json
go run ./bench/cmd/adamap-bench validate --tasks bench/tasks.example.json

# 2. Print every planned cell and the command its first attempt would run. Runs nothing.
go run ./bench/cmd/adamap-bench run --manifest bench/manifest.example.json --dry-run

# 3. Run the plan, once the owner has approved its exact run count.
go run ./bench/cmd/adamap-bench run --manifest bench/manifest.example.json \
  --records bench/records/pilot-1 --source cristiangirlea/harness=/path/to/harness \
  --i-have-approval-for-N-runs 240

# 4. Reduce the checked records into the report.
go run ./bench/cmd/adamap-bench reduce --manifest bench/manifest.example.json \
  --records bench/records/pilot-1 --out bench/RESULTS.md
```

`validate` exits 1 on any issue. Each issue is marked *structural* (the manifest is wrong) or
*blocks a run* (an unpinned identity, a candidate task, a budget short of the plan). A plan and a
dry run can still be printed while blockers remain; a real run cannot.

A real `run` refuses unless every one of these holds:

- the manifest has no issue at all;
- `--i-have-approval-for-N-runs` equals the plan's run count (tasks x conditions x backends x
  repeats), and the budget's `max_runs` covers it;
- no provider key variable is set (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`,
  `OPENAI_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY`, the Bedrock, Vertex and Foundry switches,
  endpoints and credentials (`AWS_ACCESS_KEY_ID`, `AWS_PROFILE`, `GOOGLE_APPLICATION_CREDENTIALS`
  and the like), any `*_API_KEY`). Only the names are printed, never the values;
- for a claude backend, Claude Code's user settings (`settings.json` and `.claude.json` in
  `CLAUDE_CONFIG_DIR`, or `~/.claude/settings.json` and `~/.claude.json`) set no `apiKeyHelper`,
  `awsAuthRefresh` or `awsCredentialExport`, no billing variable in their `env`, no
  `forceLoginMethod` other than `claudeai`, and hold no Console login (`primaryApiKey`). A file
  that cannot be read as JSON refuses too. Again only names are printed.

Records go to `--records`, one `<run id>.json` per run. A run whose record exists is skipped, so an
interrupted run resumes; the approval still names the whole plan. Before a run's first attempt the
runner writes `<run id>.started` beside the records, and removes it once the record is written. A
run that stops after that point (a failed check, a refused login, an interruption) may have spent
a session: its marker stays and says why, resume does not start it again, and the execution ends
with an error naming it. Look at the marker and the run's state under `--work`, then remove the
marker to run it again. Transcripts and the warm index
cache go to `--work` (default: a temp directory). Each workspace is a directory of its own in the
system temp directory, away from `--work`, and is removed after its record unless `--keep` is
given. The grader's checkout of the fix is always removed.

## The manifest

JSON, read strictly: an unknown key is an error. `manifest.example.json` is the pilot design, and
`tasks.example.json` holds its candidate tasks.

| Field | Holds |
|---|---|
| `format`, `name` | `1`, and the experiment's name, which every record repeats. |
| `scale` | `pilot` (the five conditions, at least 10 tasks covering navigation, impact, fix and control, at least 3 repeats) or `smoke` (a wiring check, labelled as one everywhere). |
| `synthetic` | A fixture: it runs only the `fake` backend, and its outputs say SYNTHETIC. |
| `conditions[]` | One per kind: `none`, `host-lsp`, `serena`, `codegraph`, `adamap`. `components` are the pinned identities (tool, indexer, schema, language server, each with an exact `version`). `mcp` is the server added to the worker. `index` is the deterministic preparation (`command`, `output` path in the workspace). `inject` is a command whose output goes into the system prompt. `tools` and `deny` change the fixed tool set. `probe` must succeed or the run is recorded as *unsupported*, which is never a baseline success. `mode` (adamap only) is `queries`, `map` or `map+queries`, so map injection and on-demand queries can be measured apart. |
| `backends[]` | `cli` (`claude`; `codex` and `gemini` validate but have no adapter yet), and an exact `version`, `model` (no alias like `sonnet`), `effort`, `plan: subscription`, `settings` (written to the CLI's settings file), and `env`. |
| `tasks[]` or `tasks_file` | The tasks, inline or in a file relative to the manifest. |
| `repeats`, `order`, `cache` | Repeats per cell; `latin-square` with a `seed`; `cold` (every run builds its index) or `warm` (repeat 1 builds and the later repeats reuse it). |
| `execution` | Held fixed across cells: `timeout_seconds`, `max_attempts` (the retry policy), `permission_mode`, the `tools` every condition has, `context_edit`, `check_timeout_seconds`. |
| `budget.max_runs` | The upper bound, at least the plan's run count. |
| `adoption` | The pre-registered rule: `candidate` (the adamap condition), `baseline` (the none condition), `min_token_reduction` (at least 0.10), `max_latency_ratio` (the acceptable trade-off, set before any run), `min_tasks`. |

A **task** has an `id`, a `status` (`candidate` until the owner approves it), a `kind`
(`navigation`, `impact`, `fix`, `control`), its `repository` (name, URL, visibility), the full
pre-fix `revision`, the `prompt`, what it `requires`, its `checks`, its `provenance` (source
`issue`, `pr` or `authored`; `ref`; `title`; the accepted solution's `fix_revision`; who selected it
and when), and its `leakage` controls. `hide_future` and `hide_labels` must be true, and
`hidden_revisions` must hold the fix revision. `hidden_paths` must not exist in the workspace, and
the prompt must not name the fix revision.

A **check** is declared before execution, with what it `needs`:

| Type | Passes when |
|---|---|
| `command` | The command, run in the workspace after the worker, exits with `expect.exit` (default 0). |
| `fix-tests` | The same, after the fix revision's files matching `paths` (default `*_test.go`) are laid over the tree, from a grader checkout made after the worker finished. |
| `answer` | The final answer holds every `expect.contains` string. |
| `no-changes` | The worker left the tree as it found it. |
| `review` | Never by itself: a human-reviewed answer, recorded with its reviewer, and never an executable gate. Every task needs at least one executable check, and a fix task needs a command or fix-tests check. |

IDs are lower-case letters, digits, `-` and `_`, unique across conditions, tasks and backends
together. A run's ID is derived from its cell, `<task>.<condition>.<backend>.r<repeat>`, so a
record cannot claim a run it is not, and a swapped task and condition names no planned run.

## What a run does

1. A fresh workspace: `git init`, then a shallow fetch of the one revision by its hash, then a
   detached checkout. No remote, branch or tag remains, so no later commit is reachable. A hidden
   revision or path found in the fresh workspace stops the whole execution.
2. The source fingerprint: the commit, plus the hash of every difference from it by content
   (tracked and untracked). A commit alone does not identify a dirty tree.
3. The condition's probe, then its index: built, or (warm) copied back from the cell's first
   build. The digest covers every file of the output. Then its injection.
4. Up to `max_attempts` attempts, each a fresh CLI session with the prompt on stdin. Each
   attempt keeps its transcript and the transcript's hash, its exit code, wall time, usage, cost
   and the tool calls and file reads its transcript shows. Failed attempts stay in the record.
5. The after fingerprint (index output excluded), an isolation check (a hidden revision that
   became reachable is a violation), and the declared checks with their outputs and hashes.

The claude adapter runs `claude -p --output-format stream-json --verbose --model <model>
--permission-mode <mode> --setting-sources user --strict-mcp-config --mcp-config <state>/mcp.json
--settings <state>/settings.json --allowedTools <fixed tools plus the condition's>
[--disallowedTools ...] [--append-system-prompt <map>]`. The condition's MCP server is the only one
the session has, and the task repository's own `.claude/settings.json` is never loaded. The runner
reads the transcript as it streams: unless the `system`/`init` event, which comes before the first
model call, gives `apiKeySource` `"none"` (the subscription login), the CLI is killed there, no
record is written and the execution stops. A missing `apiKeySource`, or a turn before the init
event, counts as API billing.

Not yet controlled, and to settle in the wiring smoke before the pilot: the CLI also reads the
user's own settings and memory files, and the worker is not sandboxed from the network or from
paths outside its workspace beyond the CLI's permission mode: keeping the other runs' state away
from the workspace hides nothing from a worker that searches the disk. Isolation records only what is
observable.

## Records and the reducer

A record carries the run's identity, the manifest's name and its synthetic flag, the source and
after fingerprints, the index (digest, the tree it was built from, reused or built, build time,
size, refreshes), every attempt, the answer and its hash, the check results with output hashes,
the isolation outcome, and any model-based overhead. It has no success field: success is
recomputed from the check outputs.

Usage is kept with its provider's semantics:

- **input-excludes-cache** (Anthropic): the counters are input, cache creation, cache reads and
  output; total = all four.
- **input-includes-cache** (OpenAI/Codex, Google): input already holds the cached reads; total =
  input + output. A cache-creation counter is refused, and a reported total that adds cached input
  again is refused.

Each usage is `reported`, `estimated` (it names its estimator; the map's bytes/4 is one) or
`unavailable` (no counters at all). Missing is not zero: a run without reported usage is counted as
unmeasured, never as a run that cost nothing. The last turn's context is kept apart from the
cumulative cache reads. Cost is `spend`, `api-equivalent` (Claude Code's own figure, which is an
API-price estimate even on a subscription) or `subscription`, and an estimate names its price
date, model and context tier.

`validate --records` and `reduce` refuse a record set with any of these problems: a duplicate,
unplanned or swapped run ID; synthetic and real records mixed; source drift (another commit, or a
dirty tree); index drift (built from another tree, or another digest within a warm cell); a check
missing, undeclared, of another type, on another tree, with a hash that does not match its output,
or whose passed flag disagrees with its output; incomplete counters; wrong cache arithmetic.

The report holds: identities and fixed settings; coverage per cell (a cell is complete when every
repeat is recorded, supported and has reported usage); success and isolation failures; p50/p95
latency; median total tokens with the reported, estimated and unavailable counts; tokens and known
cost per success, failures included in the numerator (undefined with zero successes, incomplete
when any run lacks the amount); paired differences against `none` over complete comparable cells
only; and preparation overhead. Preparation is reported as first use and as amortized over the runs
observed to use each build, never over a hypothetical reuse count. Then the tool calls and source
reads, and the adoption rule's verdict.

The adoption rule (issue #6), for adamap against none, per backend:

- **inconclusive** for a smoke, always: the rule is evaluated on the pilot only;
- **fail** on any new deterministic correctness failure (a check that passed in every baseline run
  of a task fails in an adamap run) or new isolation failure, whatever the coverage;
- **inconclusive** when any adamap or none cell is incomplete (missing coverage prevents a
  measured-savings claim), when fewer than `min_tasks` tasks are comparable, or when the
  baseline's median tokens or p50 latency is zero (nothing to compare against);
- otherwise **pass** when adamap's success is equal or better, its median total model tokens are
  at least `min_token_reduction` lower, and its p50 latency (preparation amortized at the observed
  reuse) is at most `max_latency_ratio` times the baseline's; **fail** if not.

The verdict is a pilot threshold, not statistical proof. A quality claim without token savings is
argued separately; the reducer does not evaluate one.

## Synthetic fixtures

Tests build manifests and records in code (`bench/internal/benchtest`). The `fake` backend runs
`adamap-bench fake-agent`, which answers with the prompt and prints a synthetic transcript; it
never calls a model. A synthetic manifest runs only the fake backend, its records say
`"synthetic": true`, and the record checks refuse a mix of synthetic and real records. Its report
says SYNTHETIC in the title, at the top, in every section heading and at the end, and `reduce`
refuses to write it to a file named `RESULTS.md`.

## Before the pilot

- Pin every `UNPINNED` identity in `manifest.example.json`: the CLI version, the model, gopls,
  Serena and its commit, CodeGraph and its commands, adamap's commit and scip-go.
- Review the candidate tasks in `tasks.example.json`: their prompts, revisions and checks. Set
  each one kept to `approved`. The harness tasks need `--source cristiangirlea/harness=<a local
  clone>` when the repository is private.
- Confirm `max_latency_ratio` (1.25 in the example) before any run: it is pre-registered.
- Run a wiring smoke (`"scale": "smoke"`, one task, one repeat) with its own approval, and settle
  the isolation points above.
- The full pilot, as the example manifest stands: 16 tasks x 5 conditions x 1 backend x 3 repeats =
  240 runs, each up to 2 attempts.
