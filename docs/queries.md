# adamap queries

`outline`, `find`, `refs`, `callers` and `impact` answer from the store `adamap index` wrote (see
[index.md](index.md)). Nothing is parsed at question time: the store is loaded, indexed in memory
once, and the question is a lookup.

```text
adamap outline <file>              [--repo dir] [--commit sha] [--json]
adamap find    <name>              [--repo dir] [--commit sha] [--json] [--limit n]
adamap refs    <symbol>            [--repo dir] [--commit sha] [--json]
adamap callers <symbol>            [--repo dir] [--commit sha] [--json]
adamap impact  <symbol|file>       [--repo dir] [--commit sha] [--json] [--depth n]
```

## Flags every query takes

| Flag | Default | Meaning |
|---|---|---|
| `--repo` | `.` | The repository root, where `.adamap` lives. |
| `--commit` | the commit indexed last (`.adamap/latest`) | The full hash (40 or 64 hex digits) of the store to ask. |
| `--json` | off | Print one line of JSON instead of text. |

Flags may come before or after the argument; after a bare `--` everything is the argument. A repository
without a store, or without one for `--commit`, fails with a message that says to run `adamap index`.

**Exit codes.** 0 when the question was answered, even if the answer is empty; 1 when it cannot be
answered (no store, an unknown file or symbol, an ambiguous name); 2 for a usage error. An empty
answer prints nothing on stdout and, in text mode, a one-line note on stderr. With `--json` there are
no notes: the JSON is the whole answer.

## What the arguments mean

**A file** is matched against the paths in the index, which are relative to the project root with
forward slashes. adamap converts what you type to that form first: backslashes become slashes, a
leading `./` and `..` segments are cleaned, and an absolute path is made relative to `--repo` (one
outside the repository is an error). A relative path is taken relative to the repository, not to the
working directory.

**A symbol** is its SCIP symbol string, or a display name. A name is resolved as `find` would,
but strictly: it must equal exactly one symbol's name, or, failing that, exactly one symbol's name
ignoring case. A prefix or a fragment is not a name (that is what `find` is for). A name that fits
several symbols is refused, with up to 20 of them listed with their symbol strings; pass one of those:

```text
$ adamap refs A
adamap refs: the name "A" fits 2 symbols: pass the symbol string of the one you mean
  calls/a.go:2 Function A  scip-go gomod example.com/c v1 `example.com/c/calls`/A().
  calls/b.go:19 Method A  scip-go gomod example.com/c v1 `example.com/c/calls`/Thing#A().
```

A symbol the indexer never described (a function in a library that was not indexed, say) has no display
name; its name is read out of its symbol string, so `Remote` finds `...`example.com/other`/Remote().`.

**Local symbols** (`local N` in SCIP) cannot be addressed, because the same ID is a different symbol in
each file. No query lists them, and a local definition never counts as the caller of anything.

**Lines and columns** in the output are one-based, as an editor shows them. Columns are in the
indexer's position encoding.

## Text output

One line per item, starting with where it is: `path:line kind name`.

- `kind` is the SCIP kind's name (`Function`, `Method`, `Struct`), or for `refs` the kind of use.
- A symbol defined outside the index has no place, and its line starts with `-`; one with no kind has
  `-` there: `- - Remote`.
- `callers` adds `refs=` and the lines of the references; `impact` adds `depth=`.
- A path, kind, name or symbol string that holds a control character (such as ESC), a format character
  (a bidi override, a zero-width space) or invalid UTF-8 is printed in Go quotes (`"a\x1b[2Jb"`), since
  the store may come from a hostile repository and a terminal acts on them. `--json` keeps the raw value.

## JSON output

Every answer is one JSON object on one line. Arrays are never `null`. New fields may be added;
existing ones keep their meaning. Every object starts with:

| Field | Meaning |
|---|---|
| `commit` | The full hash of the store that answered. |
| `dirty` | `true` when that store was indexed with uncommitted changes; left out otherwise. |

A **symbol** object, used throughout:

| Field | Meaning |
|---|---|
| `id` | The SCIP symbol string. |
| `name` | The display name, or the name read out of `id` when the indexer gave none. |
| `kind` | The SCIP kind's name; left out when the indexer gave none. |
| `path`, `line` | Where this entry is defined (the line of its name); left out for a symbol defined outside the index. |
| `end_line` | The last line of the construct around it when the indexer gave an enclosing range, else the last line of its name. |
| `signature` | When the indexer gave one. |

A **site** object is one occurrence: `line`, `col`, and `kind`, the first of `definition`, `import`,
`write`, `read` and `reference` that applies to its roles.

## outline

`adamap outline <file>` lists the global symbols the file defines, by line. A symbol with two
definitions in the file appears twice.

```text
$ adamap outline calls/a.go
calls/a.go:2 Function A
calls/a.go:8 Function B
calls/a.go:14 Function D
```

JSON: `commit`, `path` (the index's spelling), `symbols` (symbol objects). A file the index has but
that defines nothing gives an empty list; a file it does not have is an error.

## find

`adamap find <name> [--limit n]` lists symbols whose name fits `name`, best first. A symbol is in the
first group it fits:

1. the name equals `name`;
2. equal ignoring case;
3. starts with `name`, ignoring case;
4. contains `name`, ignoring case.

Within a group the order is the store's, by symbol string, so it is stable. `--limit` (default 25, at
least 1) cuts the list; the text mode says how many matches were left out.

JSON: `commit`, `query`, `total` (all the matches, not only the listed ones), `symbols`.

## refs

`adamap refs <symbol>` lists every occurrence of the symbol, definitions included. Files that define the
symbol come first, then the others, each group by path; inside a file the definitions come first, then
the other occurrences, each by position.

```text
$ adamap refs C
calls/b.go:2 definition C
calls/b.go:11 reference C
calls/b.go:20 reference C
calls/a.go:10 reference C
```

JSON: `commit`, `symbol`, `total`, `files`: a list of `{"path": ..., "refs": [site, ...]}`.

## callers

`adamap callers <symbol>` lists the symbols whose definition holds a reference to the symbol. Each
caller is one definition (a symbol defined in two files can appear twice), with the lines of its
references, ordered by path and line.

```text
$ adamap callers C
calls/a.go:8 Function B refs=10
calls/b.go:8 Function Rec refs=11
calls/b.go:19 Method A refs=20
```

JSON: `commit`, `symbol`, `callers`: a list of `{"symbol": symbol, "sites": [site, ...]}` where `symbol` says
where the caller is defined, and `unattributed`, the number of references that lie in no definition
(left out when 0). A reference inside the symbol's own definition makes it its own caller (recursion).

### Which definition holds a reference

SCIP records a call as a reference occurrence; which function it sits in is not recorded, so adamap works
it out from where the definitions are. Each definition of a global symbol has a scope, and a reference
belongs to the innermost scope that contains the position where the reference starts. A scope is:

1. **The enclosing range** of the definition occurrence, when the indexer gave one (the whole function,
   body and doc comment). The start is inside the scope, the end is not.
2. **Otherwise the fallback**: the span from column 0 of the line of the definition's name to column 0
   of the line of the next global definition's name in the same document, or to the end of the document
   for the last one, and clipped to the scope that holds the definition, if there is one. Starting at
   column 0 keeps what stands left of the name, such as the receiver in Go's `func (s *Server) Handle()`,
   inside the definition; a second definition on the same line starts at its name.

The fallback is a heuristic. A definition without an enclosing range is taken to own everything up to
the next definition, so a top-level statement between two functions is attributed to the first, and every
global definition (a struct field, say) ends the span of the one before it. An indexer that gives
enclosing ranges does not have these problems.

Also: the reference occurrence's own enclosing range is ignored (SCIP uses it for the expression around
the reference, such as the call, not for the function); an occurrence with the import role is not a call;
a local definition neither ends a fallback span nor counts as a caller; a reference outside every scope,
such as a package-level initializer, is counted in `unattributed` and reported on stderr in text mode.

## impact

`adamap impact <symbol|file> [--depth n]` lists what depends on the target through callers,
transitively and breadth first, nearest first: the callers of the target are at depth 1, their callers
at depth 2, and so on up to `--depth` (default 3, at least 1). Inside a depth, the order is by path and
line.

```text
$ adamap impact C
calls/a.go:8 Function B depth=1
calls/b.go:8 Function Rec depth=1
calls/b.go:19 Method A depth=1
calls/a.go:2 Function A depth=2
calls/b.go:17 Struct Thing depth=2
calls/a.go:14 Function D depth=3
```

The argument is a file when it is a path the index has a document for; otherwise it is a symbol, as
for `refs`. For a file, the walk starts from every global symbol the file defines: the file's own
symbols are the starting point and are never listed, even when they call each other.

Each symbol is listed once, at the shallowest depth it is reached, and the target is never listed, so a
cycle (a recursive function, two functions that call each other) ends the walk instead of repeating.

JSON: `commit`, `target` (the symbol string or the path), `kind` (`symbol` or `file`), `depth` (the limit
that was used), `impacted`: a list of `{"symbol": symbol, "depth": n, "via": id}` where `via` is the symbol
it calls on the way: the target itself at depth 1, otherwise a symbol at depth `n-1`.

The list is not capped. A function the whole code base calls reaches much of it within a few levels, so
lower `--depth` when only the near neighbours matter.

## Speed

`internal/query` indexes the store once per load: symbols by ID, documents by path, the occurrences of
each symbol, and the scopes of each document. A question then reads only what it asks about, and the
tests hold every query under 50 ms on a synthetic index of 200,000 occurrences (`go test -bench .
./internal/query` prints the times).
