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

CI tests pull requests and pushes to `main` on standard GitHub-hosted Linux and Windows runners,
free for a public repository; the job skips if the repository is private. No self-hosted runner is
used. See [SECURITY.md](SECURITY.md).

- **One pull request per slice**, small enough to review, with `PLAN.md` updated in the same PR.
- **Tests first, seen failing.**
- **No model calls** in the code or the tests.

Releases are the maintainer's: a `v*` tag matching `Version` in `cmd/adamap/main.go`.
