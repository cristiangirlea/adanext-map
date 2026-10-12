# Security

Report vulnerabilities privately through [GitHub's private vulnerability reporting](https://github.com/cristiangirlea/adanext-map/security/advisories/new).
Do not put credentials or private code in public issues.

The boundaries every slice keeps as it lands (PLAN.md says which have): adamap reads a SCIP index
and the repository it describes, and writes only under `.adamap/` in that repository (and, with
`adamap install` when asked, the host's MCP configuration in the current project). `adamap mcp`
speaks over stdio to the host that started it and opens no network port. adamap makes no network
calls and sends nothing anywhere.

A SCIP index is untrusted input: the reader is to bound sizes and counts and report a malformed
index instead of acting on it.

CI uses standard GitHub-hosted runners. Public contributions must not run on the maintainer's
desktop or private infrastructure. Tests need no credentials and make no model calls.
