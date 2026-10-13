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

adamap runs git with `--no-optional-locks` and with `core.fsmonitor` and `core.hooksPath` overridden,
so the `core.fsmonitor` command and the hooks in the repository's own `.git` do not run. It also
sets `GIT_NO_LAZY_FETCH=1` in git's environment, so a partial clone with a missing object fails
instead of fetching it through the repository's `remote.<name>.uploadpack` or `core.sshCommand`.
That variable needs git 2.44 or later; an older git ignores it, and a partial clone can still fetch.

`git status` also runs a clean filter, or starts a process filter, to compare a file whose
timestamp changed, and those are programs the config names. Before it, adamap lists the configured
drivers (`git config --get-regexp ^filter\.`, which runs no program) and switches each off with
empty `clean`, `smudge` and `process` commands and `required=false`. The cost: a file a driver
would change, such as a git-lfs pointer, can show as modified, so a tree can read as dirty (and the
store's header say `dirty: true`) when `git status` run by hand says it is clean. A driver whose
name has an `=` in it cannot be switched off this way, and then `adamap index` stops and asks for
`--commit`.

The drivers of a submodule are in the submodule's own config, which adamap does not list, and
`git status` would start a git inside the submodule to read them. adamap passes
`--ignore-submodules=dirty`, so git compares only the commit a submodule has checked out with the
one the superproject records and starts no git in it. The cost: uncommitted edits inside a submodule
no longer make the tree dirty (a different checked-out commit still does).

For a repository you do not trust, pass `--commit` and git is not run at all.

## Which indexer

Without `--scip`, adamap looks at the repository root and runs the first indexer that fits, with the
repository as its working directory:

| Root file | Indexer | Install it with |
|---|---|---|
| `Cargo.toml` | `rust-analyzer scip . --output <file>` | `rustup component add rust-analyzer` |
| `go.mod` | `scip-go --output <file>` | `go install github.com/scip-code/scip-go/cmd/scip-go@latest` |

The `<file>` is an `index.scip` in a new directory under the system's temporary directory
(`adamap-index-*`), so nothing is written in the repository. adamap reads it and removes the
directory when it is done, whether the indexer succeeded or not. An `index.scip` already in the
repository is neither read nor touched. If adamap is killed before it can clean up, the directory
stays in the temporary directory, where the system clears it. If the indexer is not on `PATH`,
adamap names it and says how to install it. When rust-analyzer fails, adamap also suggests
`rustup component add rust-analyzer`, since a rustup shim without that component fails the same
way. The indexer's own output goes to stderr.

For any other language, run its SCIP indexer yourself and pass the output with `--scip`.

An indexer builds the project, and building runs the project's code: `rust-analyzer` runs build
scripts and procedural macros. Index only repositories whose code you would build.

The indexers may download the project's dependencies from the network through `cargo` and the Go
module proxy, so `adamap index` can use the network even though adamap itself does not. Using
`--scip` with an index built elsewhere avoids both the dependency downloads and indexer invocations.

## What is checked

A SCIP index is untrusted input. adamap refuses, with an error naming the document and occurrence:

- a file larger than `--max-bytes`, or bytes that are not a SCIP index;
- a range that is not 3 or 4 integers, or one that is negative or ends before it starts (in either
  the packed or the typed range encoding, and for enclosing ranges too);
- a document with an empty path;
- symbol information without a symbol.

A backslash in a document path is read as `/` (SCIP specifies `/`, but indexers on Windows write
`\`), and the path is cleaned (`a/./b.go` and `a//b.go` become `a/b.go`) before it is checked.

A document whose path leaves the project (absolute: `/x`, `C:...`, a UNC path; or with a `..` that
escapes) or names the project root itself (`pkg/..`) is skipped with its occurrences and symbols,
unread, and `adamap index` prints `skipped N documents adamap cannot store (outside the project or not a safe file name)` on stderr and still
writes the store. Indexers on Windows list such a document: the test main Go builds in its cache.
A document path with a NUL or other control character, a `:`, or a Windows device name in any
segment (`nul`, `con.txt`, `pkg/aux.go`, `COM1`, `lpt¹`, `CONIN$`, `CONOUT$`; any case, trailing dots and spaces ignored)
is skipped the same way and refused at load, on every OS, so a Linux store is safe on Windows.
The checks above (ranges, symbols) apply to the documents that are kept.

Occurrences without a symbol (syntax highlighting only) are dropped. A document listed twice is
merged into one.

Bytes do not bound memory: a few bytes of protobuf or JSON (`{},`) become a whole struct once decoded,
so the decoded index takes several times the size of the file and a small file can hold millions of
items. `--max-bytes` is the bound for the index file, and counts bound what is built from it. Both the
SCIP index and the store are refused with an error naming the limit when they hold more than 2,000,000
documents, 20,000,000 symbols (global and local together, as in the header's `counts`), 50,000,000
occurrences or 20,000,000 relationships (over all symbols). These are generous: a Rust workspace of
2,000 files measured about 20,000 occurrences per 17 files, some 2.4 million occurrences in all, so
the defaults leave room for some 40,000 such files. For an index the counts are checked right after the
protobuf is parsed and before it is converted, so they bound the conversion and the store written from
it, while `--max-bytes` is what bounds the memory of reading the file. When the store is loaded they are
enforced as the JSON streams in, one element at a time, and loading stops at the first one over its
limit instead of after building them all; the counts in `header.json` are checked first, but only as a
quick refusal, since a hand-edited header proves nothing. The store format is unchanged and stores
written earlier still load. The limits are not flags.

## The store

```text
<repo>/.adamap/
  .gitignore                   `*`, so git ignores the stores; written if missing, never replaced
  latest                       the commit written last, one line
  <commit>/
    header.json                what the store holds, plain JSON
    store.json.gz              the model, gzip-compressed JSON
```

Each file is written to a temporary file in the same directory and renamed into place. Writing a
commit's store removes its `header.json` first and writes it last, so a store whose write was cut
short has no header and does not load: run `adamap index` again. A `.adamap` or `<commit>` directory
that is a symbolic link is refused, for reading and writing, so a cloned repository cannot point the
store outside itself, and so is a `header.json` or `store.json.gz` that is not a regular file; a
`header.json` over 1 MiB is refused. A document path or a symbol's definition path that is absolute
or leaves the project is refused at load. `latest` must be a regular file of a commit hash; one
that is a link or holds anything else is refused. Loading stops with an error once `store.json.gz`
inflates past 4 GiB. A store file that is replaced while it is being opened, so that what was
opened is not the file that was checked, is refused as well.

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

The file is written one document and one symbol at a time, straight into the compressor, so writing
a store holds the model and one element of it, never the whole JSON. `<`, `>` and `&` are written as
they are rather than as `\u003c`, `\u003e` and `\u0026`; both forms are the same JSON string, so
stores written before this still load, and adamap versions from before it load the new ones.

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
