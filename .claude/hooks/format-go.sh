#!/bin/bash
# PostToolUse (Edit|Write): format the edited Go file. Missing tools are skipped silently.
f=$(jq -r '.tool_input.file_path // empty')
case "$f" in *.go) ;; *) exit 0 ;; esac
[ -f "$f" ] || exit 0
if command -v gofumpt >/dev/null 2>&1; then gofumpt -w "$f" 1>&2 || exit 2
elif command -v gofmt >/dev/null 2>&1; then gofmt -w "$f" 1>&2 || exit 2
fi
command -v goimports >/dev/null 2>&1 && goimports -w "$f" >/dev/null 2>&1
exit 0
