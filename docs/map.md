# adamap map

`adamap map` prints the files and symbols that matter most in a repository, small enough to put in
an agent's context at the start of a task, in the spirit of Aider's repo map. It reads the store
`adamap index` wrote; nothing is parsed or indexed at question time.

```text
adamap map [--tokens n] [--for words] [--repo dir] [--commit sha] [--json]
```

| Flag | Default | Meaning |
|---|---|---|
| `--tokens` | 1024 | The most tokens the map takes, by the estimate below. Must be positive. |
| `--for` | none | Words of the task: files whose path, or the name of a symbol they define, contain one rank higher. |
| `--repo` | `.` | The repository root, whose `.adamap` directory holds the store. |
| `--commit` | the store written last | The full hash of the commit to map. |
| `--json` | off | Print the map as JSON (below) instead of text. |

The exit code is 0 on success, 1 when the store cannot be read (none yet says to run
`adamap index`), and 2 for a usage error. `adamap map -h` prints the flags. At most one note goes
to stderr, with exit 0: the first of these that applies, in this order. `--for` matches nothing
(the map is then not personalised); the store defines no symbols; not even one symbol fits in
`--tokens`. A later note is not printed beside an earlier one: with `--for` matching nothing and a
`--tokens` too small for one symbol, only the first is said.

## The output

```text
cmd/adamap/main.go
  func  run(args []string, stdout, stderr io.Writer) int
  const  Version
internal/store/store.go
  func  Load(repo, commit string) (Header, *model.Index, error)
  struct  Header
```

A file's path on a line of its own, then one line for each of its symbols, indented, most important
first: a label for its kind, then its detail.

- The label is `func`, `method`, `type`, `var`, `const`, or the SCIP kind in lower case (`struct`,
  `interface`, `enum`, `trait` ...). Without a kind, it is read off the symbol: `func` for a
  method descriptor, `type` for a type, `macro` for a macro, `sym` otherwise.
- The detail is the signature from the name on, leading keywords dropped (`func Add(a, b int) int`
  shows as `Add(a, b int) int`, `pub fn run()` as `run()`, a Go method keeps its receiver).
  Without a signature, it is the name and the first line of the doc (`Hub  // Hub joins the
  spokes.`); without either, the name. It is one line, white space collapsed, at most 120 bytes,
  ending in `...` when cut.

Files are in rank order. A file that defines nothing the map shows is left out, and so are local
symbols, namespaces (packages, modules) and parameters. Fields and enum members (the SCIP kinds
`Field`, `StaticField`, `StaticDataMember`, `EnumMember`, `Property` and `StaticProperty`) are not
shown either: the line of the struct, class or enum that holds them is the one to read, and a
member would take a line of the budget from a symbol that matters more. A file that defines only
members is left out. References to a member still link the files, so they count in the ranking
(see below); `adamap find`, `outline` and `refs` still list members.

A path is printed quoted, so each line stays one line and shows what it holds, when it holds a
control or format character (bidi overrides, zero-width characters) or a line or paragraph
separator (U+2028, U+2029), when it is not UTF-8, or when it starts with a space (a symbol line
starts with two, so such a path would pass for one). The same characters in a name, signature or
doc become a space.

## How it ranks

1. **The reference graph.** Each file is a node. File A links to file B when A references a symbol
   B defines. Each such symbol adds the square root of its reference count to the edge's weight, so
   one symbol A uses fifty times weighs about 7, not 50, and a file is not ranked on one hot
   symbol. References within a file, local symbols, namespaces and parameters add nothing.
2. **PageRank** over that graph, damping 0.85, iterated until the ranks change by less than 1e-6
   in total or 100 times. A file that references nothing spreads its rank over all files alike.
   Files that many files use, or that important files use, rank high.
3. **Personalisation.** Without `--for`, the random jump of PageRank lands on every file alike.
   With it, the words are split at white space and commas and compared without case; a file gets
   one share of the jump for each word its path, or the name of a symbol it defines, contains.
   Those files, and the files they use, move up. The jump is 15% of the rank, so a matched file
   rises but does not always lead. When no file matches, the map is the unpersonalised one.
4. **Test files.** The rank of a test file is multiplied by 0.25 once PageRank is done, so tests
   take a smaller share of the budget than the code they test. A test file is one named
   `*_test.go`, `*.test.ts` or `*.spec.ts` (also `.tsx`, `.js`, `.jsx`), or under a directory named
   `tests` or `test` at any depth. This is by path only: a Rust `#[cfg(test)]` module inside a
   source file is not told apart. A test file's references still count in step 1, so the code it
   exercises keeps the rank those references give it. The ranks `rank` shows (JSON below) sum to 1
   only when no file is a test file.
5. **Symbols.** A file's rank is shared among the symbols it shows (not its fields and enum
   members) in proportion to the references each gets from other files, plus one. That share is
   the symbol's score.

Ties go to the path, then the symbol name, then the symbol, so the same store always gives the same
map.

## The budget

The token estimate is the map's text in bytes divided by 4, rounded up. It is an estimate:
tokenisers differ, and code and paths often take more tokens per byte than prose.

The map is filled by symbol score across all files, highest first, as Aider's is: each symbol
costs its line, plus its file's path line the first time a symbol of that file is taken. Filling
stops at the first symbol whose lines would take the text over `--tokens`, so the map never exceeds
the budget by the estimate, and an exact fit is taken. The chosen symbols are then printed under
their files in rank order. A file's chosen symbols are always its top ones, but a lower-ranked file
can show before a higher one has shown all of its symbols: one file with a single important symbol
outscores each of a large file's many.

## Tuning

- `--tokens`: 1024 shows the core of a small project. Raise it for a larger repository or a
  first look, lower it to leave room for the task. The map only grows with the budget.
- `--for`: name what the task is about (`--for "store load header"`). Words that match nearly every
  path (`go`, `src`) personalise nothing in particular.
- `--commit`: map an older commit whose store you kept, to compare.

## JSON

`--json` prints the same content, the same files and symbols chosen within the same budget, as one
object:

```json
{
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "tokens": 9,
  "estimate": 9,
  "files": [
    {
      "path": "pkg/a.go",
      "rank": 0.6491228788227412,
      "symbols": [
        {
          "id": "scip-go gomod example.com/m v1 `example.com/m/pkg`/Add().",
          "kind": "func",
          "name": "Add",
          "detail": "Add(a, b int) int",
          "refs": 1,
          "score": 0.43274858588182746
        }
      ]
    }
  ]
}
```

That is `adamap map --tokens 9 --json` on the test fixture, whose text form is:

```text
pkg/a.go
  func  Add(a, b int) int
```

| Field | Meaning |
|---|---|
| `commit` | The commit mapped. |
| `tokens` | The budget, `--tokens`. |
| `estimate` | The text form's tokens by the estimate: the JSON itself is larger. |
| `for`, `matched` | The words of `--for` and how many files they matched; left out without `--for` or a match. |
| `files` | The files in rank order, an empty list when nothing is shown. |
| `path`, `rank` | The file's path and its PageRank, times 0.25 for a test file (the ranks sum to 1 when no file is a test file). |
| `symbols` | The file's chosen symbols, most important first. |
| `id` | The SCIP symbol, as the store holds it. |
| `kind`, `detail` | The label and the detail, as the text shows them. |
| `name` | The display name, or the last name in `id` when the indexer gave none. |
| `refs` | References to the symbol from other files. |
| `score` | The symbol's share of its file's rank. |
