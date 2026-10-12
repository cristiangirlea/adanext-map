#!/bin/sh
# After an edit: gofmt must print nothing, as CI requires.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v gofmt >/dev/null 2>&1 || exit 0
# Tracked and new files only, so a worktree or a .adamap store under the repository is not walked.
unformatted=$(git ls-files -co --exclude-standard -- '*.go' | xargs -r gofmt -l 2>/dev/null)
if [ -n "$unformatted" ]; then
  printf 'gofmt would change these files (run gofmt -w on them):\n%s\n' "$unformatted" >&2
  exit 2
fi
exit 0
