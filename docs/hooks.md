# Repository hooks

The PreToolUse push guard in `.claude/hooks/push-guard.sh` checks Bash and PowerShell
commands before they run. It refuses pushes to main or tags, force pushes, tag and release
writes, automatic or administrative PR merges, and mutating release or git-ref API calls. A refusal
exits 2 and prints a one-line reason.

The guard uses ASCII unit separator (`\037`) internally so word splitting works under macOS's
Bash 3.2 as well as Linux sh and Git Bash on Windows.

For commands it inspects, the guard refuses JSON Unicode escapes other than `\u0026`, `\u0027`,
`\u003c` and `\u003e` (hex letters may be uppercase), and refuses backspace and form-feed
escapes. Bash commands with carriage-return escapes are refused; PowerShell carriage returns
and CRLF are treated as line endings. In cwd, only escaped backslashes, quotes and slashes are
accepted. These checks prevent decoding differences from hiding a guarded command. A raw unit
separator is refused too. Literal backslashes followed by escape-like text remain literal.

The parser treats `<<` in `$[...]`, `$((...))` and `((...))` arithmetic as a shift,
including letter operands; nested brackets in `$[...]` are refused. Unquoted `let x=1<<n`
uses heredoc syntax. At the end of a heredoc command line, any shell or `ssh` word, including
a consumer after the delimiter or in another pipeline stage, makes the body executable
even with quoted delimiters. Shell bodies are scanned as commands, and `ssh` bodies as remote
commands. Remote pushes are refused when repository state is needed: local Git configuration cannot resolve a remote destination.
Text heredocs remain text; unquoted bodies containing substitutions are inspected as before.
Directory changes inside parentheses and command substitutions are restored at the closing token.

ANSI-C quotes support escaped apostrophes and backslashes; other ANSI-C escapes are refused.
In cmd command text, `#` is literal and `^` escapes the next unquoted character; quoted carets are
refused, as are dangling carets and caret line continuations. Literal PowerShell here-strings
are accepted only with known data consumers (`Write-Output`, `Write-Host`, `Set-Content`,
`Out-File`, `Add-Content`) or variable assignments of data. Code execution syntax, unknown
consumers, expanding double here-strings, and malformed delimiters are refused when inspected.

GraphQL endpoints are matched case-insensitively after removing a URL scheme, host and leading
slashes; any endpoint ending in `graphql`, including `api/graphql`, gets the same checks.
GraphQL API calls must contain a visible read-only query. Mutations (including `createRef` and
`updateRef`), query files, and hidden queries are refused even with `--method GET`.
`git tag -l` and `git tag --list` allow patterns and known read-only formatting/sorting options;
unknown options and tag writes are refused. `gh release list` and `gh release view` are allowed;
other release operations remain refused.

Run `sh .claude/hooks/push-guard.test.sh` for the regressions. The public-only CI matrix runs
all hook tests on GitHub-hosted Linux, Windows and macOS runners. The remaining gaps tracked by
adanext-exec #23, #24 and #25 are outside this port.
