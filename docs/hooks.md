# Repository hooks

The PreToolUse push guard in `.claude/hooks/push-guard.sh` checks Bash and PowerShell
commands before they run. It refuses pushes to main or tags, force pushes, git tag commands,
gh release commands, automatic or administrative PR merges, and mutating release or git-ref API
calls. A refusal exits 2 and prints a one-line reason.

The guard uses ASCII unit separator (`\037`) internally so word splitting works under macOS's
Bash 3.2 as well as Linux sh and Git Bash on Windows.

For commands it inspects, the guard refuses JSON Unicode escapes other than `\u0026`, `\u0027`,
`\u003c` and `\u003e` (hex letters may be uppercase), and refuses backspace and form-feed
escapes. Bash commands with carriage-return escapes are refused; PowerShell carriage returns
and CRLF are treated as line endings. In cwd, only escaped backslashes, quotes and slashes are
accepted. These checks prevent decoding differences from hiding a guarded command. A raw unit
separator is refused too. Literal backslashes followed by escape-like text remain literal.

Run `sh .claude/hooks/push-guard.test.sh` for the regressions. The public-only CI matrix runs
all hook tests on GitHub-hosted Linux, Windows and macOS runners. The remaining gaps tracked by
adanext-exec #23, #24 and #25 are outside this port.
