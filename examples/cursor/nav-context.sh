#!/usr/bin/env bash
# Cursor beforeSubmitPrompt hook — run nav semantic search for the user's prompt.
#
# Install:
#   mkdir -p .cursor/hooks
#   cp examples/cursor/hooks.json .cursor/
#   cp examples/cursor/nav-context.sh .cursor/hooks/
#   chmod +x .cursor/hooks/nav-context.sh
#
# Set NAV_PROJECT to the same name you used with `nav index`.

set -u

NAV_PROJECT="${NAV_PROJECT:-myapp}"
NAV_TOP="${NAV_TOP:-5}"
NAV_BIN="${NAV_BIN:-nav}"

input="$(cat)"

if ! command -v jq >/dev/null 2>&1; then
  echo '{"continue": true}'
  exit 0
fi

prompt="$(printf '%s' "$input" | jq -r '.prompt // empty' 2>/dev/null)"
if [[ -z "$prompt" ]]; then
  echo '{"continue": true}'
  exit 0
fi

if ! command -v "$NAV_BIN" >/dev/null 2>&1; then
  echo '{"continue": true}'
  exit 0
fi

# Produces <nav-context> on stdout (used by Claude Code; Cursor may log it).
# Fail-open: never block the user's prompt.
"$NAV_BIN" hook run "$NAV_PROJECT" --type claude --top "$NAV_TOP" --query "$prompt" >/dev/null 2>&1 || true

echo '{"continue": true}'
exit 0
