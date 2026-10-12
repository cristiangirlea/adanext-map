# adamap index

`adamap index` turns a SCIP index into adamap's own store, kept per commit under
`<repo>/.adamap/<commit>/`. The queries read that store; nothing is parsed at question time.

```text
adamap index [--scip path] [--commit sha] [--repo dir] [--max-bytes n]
```

| Flag | Default | Meaning |
|---|---|---|
| `--scip` | none | Read this SCIP index instead of running an indexer. |
| `--commit` | git's `HEAD` | The full hash (40 or 64 hex digits) of the commit the index describes. |
| `--repo` | `.` | The repository root. The store goes in its `.adamap` directory. |
| `--max-bytes` | 1073741824 (1 GiB) | Refuse an index file larger than this. |

On success it prints one line to stdout, such as:

```text
indexed 0123456789ab: 2 documents, 7 symbols, 9 occurrences in 41ms -> .adamap/0123456789abcdef...
```

The exit code is 0 on success, 1 when indexing fails, and 2 for a usage error. `adamap index -h`
prints the flags.

## Which commit

Without `--commit`, adamap asks git for `HEAD` in `--repo`. If tracked files have uncommitted
changes, the store is still filed under `HEAD`, but its header says `dirty: true` and the summary
line says `(dirty)`. Untracked files do not count. With `--commit`, git is not asked: the index is
taken to describe that commit, as the caller says.

## Which indexer

Without `--scip`, adamap looks at the repository root and runs the first indexer that fits, with the
repository as its working directory:

| Root file | Indexer | Install it with |
|---|---|---|
| `Cargo.toml` | `rust-analyzer scip .` | `rustup component add rust-analyzer` |
| `go.mod` | `scip-go` | `go install github.com/scip-code/scip-go/cmd/scip-go@latest` |

The indexer writes `index.scip` in the repository. adamap reads it and then deletes it. If an
`index.scip` is already there, adamap stops instead of overwriting it: pass it with `--scip`, or
move it. If the indexer is not on `PATH`, adamap names it and says how to install it. The indexer's
own output goes to stderr.

For any other language, run its SCIP indexer yourself and pass the output with `--scip`.

An indexer builds the project, and building runs the project's code: `rust-analyzer` runs build
scripts and procedural macros. Index only repositories whose code you would build.

## What is checked

A SCIP index is untrusted input. adamap refuses, with an error naming the document and occurrence:

- a file larger than `--max-bytes`, or bytes that are not a SCIP index;
- a range that is not 3 or 4 integers, or one that is negative or ends before it starts (in either
  the packed or the typed range encoding, and for enclosing ranges too);
- a document path that is empty or absolute (`/x`, `\x`, `C:...`), or has a `..` segment;
- symbol information without a symbol.

Occurrences without a symbol (syntax highlighting only) are dropped. A document listed twice is
merged into one.

`--max-bytes` bounds the file, not the memory: the decoded index takes several times its size.

## The store

```text
<repo>/.adamap/
  latest                       the commit written last, one line
  <commit>/
    header.json                what the store holds, plain JSON
    store.json.gz              the model, gzip-compressed JSON
```

Each file is written to a temporary file in the same directory and renamed into place. Writing a
commit's store removes its `header.json` first and writes it last, so a store whose write was cut
short has no header and does not load: run `adamap index` again. A `.adamap` or `<commit>` directory
that is a symbolic link is refused, for reading and writing, so a cloned repository cannot point the
store outside itself.

### header.json

| Field | Meaning |
|---|---|
| `format` | The store format, an integer: 1 for now. |
| `adamap` | The version of adamap that wrote the store. |
| `commit` | The full commit hash. |
| `dirty` | `true` when tracked files had uncommitted changes; left out otherwise. |
| `indexer` | `name` and `version` of the indexer, from the SCIP metadata. |
| `project_root` | The project root from the SCIP metadata, a URI. |
| `created` | When the store was written, UTC, RFC 3339. |
| `counts` | `documents`, `symbols` (global and local) and `occurrences`. |

An adamap that finds a newer `format` than it reads refuses the store, naming the adamap version
that wrote it. Upgrade to that version or later, or rebuild the store with `adamap index`. Fields
are only ever added, with `omitempty`, so a store from an earlier adamap still loads.

### store.json.gz

One JSON object:

- `tool`, `project_root`: as in the header.
- `documents`: sorted by `path`, which is relative to the project root as the indexer gave it
  (SCIP asks for forward slashes). Each has a `language`, its `locals`, and its `occurrences`
  sorted by range.
- `symbols`: the global symbols, sorted by `id`.

A symbol has an `id` (the SCIP symbol string), a `display_name` and a `kind` (the SCIP kind's name,
such as `Function`). `doc` is the first line of prose in its documentation, skipping code fences.
`signature`, when the indexer gives one. `definition` is a `path` and a `range`, taken from its
first definition occurrence; it is left out for a symbol defined outside the index. `relationships`
lists other symbols with the flags `implementation`, `reference` and `type_definition`.

Local symbols (`local N` in SCIP) are kept in their document's `locals`: the same `local 0` in two
documents is two symbols.

An occurrence has a `range`, a `symbol`, its `roles` and, when the indexer gives one, an `enclosing`
range (the whole construct around it, such as a function body around its name). A range is
`start_line`, `start_col`, `end_line` and `end_col`: zero-based, the end exclusive, columns in the
indexer's position encoding.

`roles` is a bit set, and the values are fixed:

| Bit | Role |
|---|---|
| 1 | definition |
| 2 | reference (every occurrence that is not a definition, forward declarations included) |
| 4 | import |
| 8 | write |
| 16 | read |
