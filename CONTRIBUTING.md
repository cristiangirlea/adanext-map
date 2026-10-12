# Contributing to adamap

[← README](README.md)

## Build from source

Use the Go version pinned in `go.mod`:

```bash
git clone https://github.com/cristiangirlea/adanext-map.git
cd adanext-map
go build ./cmd/adamap
go test ./...
```

This produces `adamap` on Linux and macOS or `adamap.exe` on Windows.

## Checks and pull requests

Read [AGENTS.md](AGENTS.md) for the checks, the rules of this repository and the review and merge
conditions before changing code. Tests are to build their SCIP fixtures in code and need no
indexer, key or network.

CI runs pushes and the repository's own pull requests on the maintainer's lab runner (on GitHub's
hosted runner until that runner exists); a pull request from a fork runs on GitHub's hosted runner
and waits for a maintainer's approval first.
See [SECURITY.md](SECURITY.md).

- **One pull request per slice**, small enough to review, with `PLAN.md` updated in the same PR.
- **Tests first, seen failing.**
- **No model calls** in the code or the tests.

Releases are the maintainer's: a `v*` tag matching `Version` in `cmd/adamap/main.go`.
