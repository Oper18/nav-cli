# Cursor integration

nav can install a **Cursor project hook** that runs semantic search on every prompt and injects matching symbols into the agent context.

## Prerequisites

- `nav` on your `PATH`
- `nav init` completed and `OPENROUTER_API_KEY` in `~/.nav-cli/credentials`
- Qdrant running (see [README](README.md#qdrant-integration))
- Project indexed: `nav index <project> --path <repo-root>`

## Install the Cursor hook

From your repository root:

```bash
nav hook install --type cursor --path .
```

Or pass the project name explicitly:

```bash
nav hook install myapp --type cursor --path ~/work/myapp
```

This writes `.cursor/hooks.json` with a `beforeSubmitPrompt` entry that runs:

```text
nav hook run <project> --type cursor --top 5
```

### Global hook (all workspaces)

```bash
nav hook install myapp --type cursor --global
```

Installs into `~/.cursor/hooks.json` instead of the project `.cursor/` directory.

## Uninstall

```bash
nav hook uninstall --type cursor --path .
```

## How it works

1. You send a prompt in Cursor Agent.
2. Cursor invokes `nav hook run <project> --type cursor` and passes the prompt JSON on **stdin**.
3. nav embeds the prompt, searches Qdrant, and builds a `<nav-context>` block (same formatter as Claude Code).
4. nav prints a JSON response on **stdout**:

   ```json
   {"continue": true, "additional_context": "<nav-context ...>...</nav-context>"}
   ```

5. Cursor continues with the prompt; context is attached when supported by the hook runtime.

If search fails (missing index, Qdrant down, API error), nav returns `{"continue": true}` so your prompt is never blocked.

## Manual test

```bash
printf '%s\n' '{"prompt":"where is hook install implemented"}' \
  | nav hook run nav-cli --type cursor --top 3
```

## Configuration

Cursor hooks reuse the Claude hook settings in `~/.nav-cli/config.yaml`:

```yaml
hooks:
  claude_top_k: 5
  claude_min_score: 0.72
  claude_max_tokens: 4000
```

Override top-K at install time via config, or pass `--top` when running manually:

```bash
nav hook run myapp --type cursor --top 10 --query "authentication middleware"
```

## Related commands

| Command | Purpose |
|---------|---------|
| `nav index <project> --path .` | Build / refresh the semantic index |
| `nav search "<query>" <project>` | Search from the terminal |
| `nav hook install --type git --path .` | Keep index updated on commit |
| `nav hook install --type claude` | Same search injection for Claude Code |

See [README.md](README.md) for full architecture and API reference.
