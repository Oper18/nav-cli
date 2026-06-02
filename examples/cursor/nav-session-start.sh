#!/usr/bin/env bash
# Cursor sessionStart hook — inject nav project overview + a broad code search seed.
#
# Merge hooks-session-start.json into .cursor/hooks.json or copy its sessionStart block.

set -u

NAV_PROJECT="${NAV_PROJECT:-myapp}"
NAV_BIN="${NAV_BIN:-nav}"
README="$HOME/.nav-cli/projects/${NAV_PROJECT}/readme.md"

parts=()

if [[ -f "$README" ]]; then
  parts+=("## nav project overview (${NAV_PROJECT})"$'\n'"$(head -c 8000 "$README")")
fi

if command -v "$NAV_BIN" >/dev/null 2>&1; then
  block="$("$NAV_BIN" hook run "$NAV_PROJECT" --type claude --top 3 --query "project architecture entry points" 2>/dev/null || true)"
  if [[ -n "$block" ]]; then
    parts+=("$block")
  fi
fi

if [[ ${#parts[@]} -eq 0 ]]; then
  echo '{}'
  exit 0
fi

context="$(printf '%s\n\n' "${parts[@]}")"

if command -v jq >/dev/null 2>&1; then
  jq -n --arg ctx "$context" '{additional_context: $ctx}'
else
  echo '{}'
fi
exit 0
