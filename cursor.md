# Cursor integration for nav

Use **nav** to keep a semantic index of your repository and wire it into **Cursor** via [project hooks](https://cursor.com/docs/hooks). On each prompt (or at session start), nav can search Qdrant for symbols relevant to what you are asking and surface that context to the agent.

This guide covers install, first index, hook setup, daily use, and troubleshooting.

---

## What you get

| Piece | Role |
|-------|------|
| `nav index` | Parse the repo (tree-sitter), summarize symbols (OpenRouter), embed, store in Qdrant |
| `nav search` | Natural-language lookup over indexed symbols |
| `nav hook run --type claude` | Format top-K hits as a `<nav-context>` block (same path Claude Code uses) |
| Cursor `.cursor/hooks.json` | Run nav when you send a prompt or start a session |

nav does **not** ship a `nav hook install --type cursor` command yet. You copy the example files under [`examples/cursor/`](examples/cursor/) into your project’s `.cursor/` directory.

---

## Prerequisites

- **Go 1.22+** (to build nav) or a prebuilt `nav` on your `PATH`
- **Qdrant** (local Docker or Qdrant Cloud)
- **OpenRouter API key** (LLM summaries + embeddings)
- **jq** (hook scripts parse JSON from stdin)
- **Cursor** with Hooks enabled (Settings → Hooks)

---

## 1. Install nav

### Option A — build from this repo

```bash
git clone https://github.com/Oper18/nav-cli.git
cd nav-cli
go build -o nav ./cmd
install -m 755 nav ~/.local/bin/nav   # or another directory on PATH
```

### Option B — install script

```bash
cd nav-cli
./install.sh   # builds nav and moves it to ~/.local/bin/
```

Confirm:

```bash
nav --help
```

---

## 2. Bootstrap config and credentials

```bash
nav init
```

This creates `~/.nav-cli/config.yaml` and `~/.nav-cli/credentials`.

Edit **`~/.nav-cli/credentials`**:

```dotenv
OPENROUTER_API_KEY=sk-or-...
QDRANT_API_KEY=          # optional for local Qdrant without auth
```

Point Qdrant at your instance in **`~/.nav-cli/config.yaml`** (defaults are fine for local Docker on port 6334):

```yaml
qdrant:
  host: localhost
  port: 6334
  use_tls: false
```

### Start Qdrant locally (example)

```bash
docker run -d --name qdrant \
  -p 6333:6333 -p 6334:6334 \
  -v ~/.nav-cli/qdrant_storage:/qdrant/storage \
  qdrant/qdrant
```

---

## 3. Index your project (required before search/hooks help)

Use your **project name** (logical id for the Qdrant collection `nav_<project>`) and the **repository root**:

```bash
cd ~/work/myapp
nav index myapp --path .
```

Parser-only check (no LLM / no Qdrant writes):

```bash
nav index myapp --path . --dry-run
```

Force full reindex after changing embedding model:

```bash
nav index myapp --path . --force
```

Verify search:

```bash
nav search "where is authentication handled" myapp --top 5
nav search "database migrations" myapp --json
```

---

## 4. Install Cursor hooks in your repo

From your **application repository** (not necessarily the nav-cli clone):

```bash
mkdir -p .cursor/hooks
cp /path/to/nav-cli/examples/cursor/hooks.json .cursor/
cp /path/to/nav-cli/examples/cursor/nav-context.sh .cursor/hooks/
chmod +x .cursor/hooks/nav-context.sh
```

Edit **`.cursor/hooks/nav-context.sh`** and set:

```bash
NAV_PROJECT="myapp"    # same name you passed to `nav index`
```

Ensure `nav` is on the `PATH` Cursor uses when it runs hooks (often the same as your login shell; `~/.local/bin` is a common install location).

### What the example hook does

1. Cursor calls **`beforeSubmitPrompt`** with JSON on stdin (`prompt`, `workspace_roots`, …).
2. The script reads the prompt and runs:

   ```bash
   nav hook run "$NAV_PROJECT" --type claude --top 5 --query "$prompt"
   ```

3. nav prints a **`<nav-context>`** block to stdout (symbols, scores, summaries, code snippets).
4. The script returns **`{"continue": true}`** so your prompt is never blocked (fail-open).

Hook config (`.cursor/hooks.json`):

```json
{
  "version": 1,
  "hooks": {
    "beforeSubmitPrompt": [
      {
        "command": ".cursor/hooks/nav-context.sh",
        "timeout": 15,
        "failClosed": false
      }
    ]
  }
}
```

Paths are relative to the **project root** where `.cursor/hooks.json` lives.

Reload: save `hooks.json` or restart Cursor. Confirm in **Settings → Hooks** or the **Hooks** output channel.

---

## 5. Optional: session-start context

If you want nav context once per chat instead of on every prompt, add a **`sessionStart`** hook (see [`examples/cursor/hooks-session-start.json`](examples/cursor/hooks-session-start.json) and [`nav-session-start.sh`](examples/cursor/nav-session-start.sh)).

`sessionStart` supports **`additional_context`** in hook output more reliably than `beforeSubmitPrompt` in current Cursor builds. You can use both hooks together: session overview at start, per-prompt search on submit.

---

## 6. Cursor vs Claude Code hooks

| | Claude Code | Cursor (this guide) |
|---|-------------|---------------------|
| Install | `nav hook install --type claude --project myapp` | Copy `examples/cursor/*` into `.cursor/` |
| Config file | `.claude/settings.json` | `.cursor/hooks.json` |
| Run nav | `nav hook run myapp --type claude --query "$CLAUDE_USER_PROMPT"` | Same command; prompt from hook stdin JSON |
| Context injection | Supported via `UserPromptSubmit` | **`beforeSubmitPrompt`**: nav runs and logs; full prompt injection is limited in current Cursor versions — prefer **`sessionStart`** + `additional_context` for stable injection |

Git pre-commit indexing is unchanged:

```bash
nav hook install --type git --path ~/work/myapp
```

---

## 7. Daily workflow (examples)

```bash
# After a large refactor
nav index myapp --path ~/work/myapp

# Quick lookup from the terminal
nav search "retry logic for API client" myapp --top 3

# Skip nav on a WIP commit, sync later
NAV_SKIP=1 git commit -m "wip"
nav sync myapp --path ~/work/myapp --since HEAD~5
```

In Cursor: open the repo, send a normal agent prompt — the hook runs nav search in the background. Check the Hooks output channel if results look empty (usually missing index or API keys).

---

## 8. Manual hook test (no Cursor UI)

From the project root:

```bash
printf '%s\n' '{"prompt":"where is the index command implemented","workspace_roots":["/home/you/work/myapp"]}' \
  | .cursor/hooks/nav-context.sh
```

Expect exit code `0` and JSON like:

```json
{"continue": true}
```

Run nav directly to see the context block:

```bash
nav hook run myapp --type claude --top 5 --query "where is the index command implemented"
```

---

## 9. Configuration knobs

In `~/.nav-cli/config.yaml`:

```yaml
hooks:
  claude_top_k: 5           # default top-K for `nav hook run --type claude`
  claude_min_score: 0.72    # drop weak matches
  claude_max_tokens: 4000   # cap injected text size
```

Override per invocation:

```bash
nav hook run myapp --type claude --top 3 --query "payment webhook"
```

---

## 10. Troubleshooting

| Symptom | Fix |
|---------|-----|
| `Missing Authentication header` on index/search | Set `OPENROUTER_API_KEY` in `~/.nav-cli/credentials` |
| Qdrant connection refused | Start Qdrant; match `host`/`port` in config |
| Hook never runs | Valid `.cursor/hooks.json`; script executable; restart Cursor |
| Empty search / no context | Run `nav index <project> --path <repo>` first |
| Indexing entire home directory by mistake | Always pass `--path` to a single repo root |
| `nav: command not found` in hook | Use full path in script or add `~/.local/bin` to PATH for GUI apps |

---

## 11. Uninstall Cursor hooks

```bash
rm .cursor/hooks.json .cursor/hooks/nav-context.sh
# optional: rmdir .cursor/hooks .cursor if empty
```

This does not delete the Qdrant index. To remove indexed data, delete the collection in Qdrant or reindex after clearing.

---

## Related docs

- [README.md](README.md) — full nav architecture and commands
- [Claude Code integration](README.md#claude-code-integration) — `nav hook install --type claude`
- [Cursor Hooks documentation](https://cursor.com/docs/hooks)
