# Security

Report vulnerabilities privately through [GitHub's private vulnerability reporting](https://github.com/cristiangirlea/adanext-map/security/advisories/new).
Do not put credentials or private code in public issues.

adamap reads a SCIP index and the repository it describes, and writes only under `.adamap/` in
that repository (and, with `adamap install` when asked, the host's MCP configuration in the
current project). `adamap mcp` speaks over stdio to the host that started it and opens no network
port. adamap makes no network calls and sends nothing anywhere.

A SCIP index is untrusted input: the reader bounds sizes and counts and reports a malformed index
instead of acting on it.

CI runs on the maintainer's lab runner for pushes and the repository's own pull requests; a fork's
pull request runs on GitHub's hosted runner after a maintainer's approval and never on the lab.
