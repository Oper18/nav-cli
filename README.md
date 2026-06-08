# nav

CLI tool for parsing source code repositories into semantically rich, searchable code units — and keeping them fresh as the codebase evolves.

`nav` slices a repository into functions, methods, and classes using tree-sitter, enriches each unit with an LLM-generated summary via OpenRouter, converts the result into vector embeddings, and stores everything in Qdrant. Two integration points keep the index alive: a git pre-commit hook that patches changed symbols on every commit, and a Claude Code hook that injects relevant code context into every AI-assisted session.

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Directory Layout](#directory-layout)
- [Config Directory Layout](#config-directory-layout)
- [Code Unit Schema](#code-unit-schema)
- [Text Representation](#text-representation)
- [Initialization](#initialization)
- [Command Reference](#command-reference)
- [Language Support](#language-support)
- [LLM Providers (OpenRouter)](#llm-providers-openrouter)
- [Embedding Providers](#embedding-providers)
- [Qdrant Integration](#qdrant-integration)
- [Git Hook Integration](#git-hook-integration)
- [Claude Code Integration](#claude-code-integration)
- [Development](#development)

---

## Overview

```
nav index --project mokosh --path ~/work/mokosh
```

This single command:

1. Walks the repository, detects the language per file, and skips vendor/generated code.
2. Parses each file with tree-sitter and extracts named symbols (functions, methods, classes).
3. Sends each symbol's source to an OpenRouter model for a one-line summary and tag inference.
4. Builds a human-readable text block from the structured metadata.
5. Encodes the text block into a dense vector using a configurable embedding model.
6. Upserts the vector + structured payload into Qdrant.

After indexing, `nav search` lets you query the index in plain language:

```
nav search --project mokosh "password hashing and user creation"
```

The git hook updates only the symbols touched by a commit, so incremental cost is low. The Claude Code hook intercepts every user prompt, runs a `nav search` against it, and prepends the top-K results to the assistant context.

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                      nav CLI                         │
│  cmd/                                                │
│    main.go  →  internal/cli/                         │
│                  root.go   index.go   search.go      │
│                  hook.go   sync.go    config.go      │
└──────────────┬───────────────────────────────────────┘
               │
       ┌───────▼────────────────────────────────────┐
       │              internal/                     │
       │                                            │
       │  parser/          embedding/    store/     │
       │  ├─ detect.go     ├─ client.go  ├─ qdrant.go│
       │  ├─ treesitter.go ├─ nvidia.go  └─ schema.go│
       │  └─ extract.go    ├─ qwen.go               │
       │                   └─ openai.go             │
       │                                            │
       │  llm/             hook/        config/     │
       │  ├─ client.go     ├─ git.go    └─ config.go│
       │  ├─ openrouter.go └─ claude.go             │
       │  └─ prompts.go                             │
       └────────────────────────────────────────────┘
               │                   │
       ┌───────▼──────┐   ┌────────▼───────┐
       │  OpenRouter  │   │    Qdrant      │
       │  (LLM + sum) │   │  (vectors +    │
       └──────────────┘   │   payload)     │
                          └────────────────┘
```

**Data flow for `nav index`:**

```
File system
  └─ detect language (parser/detect.go)
       └─ tree-sitter parse (parser/treesitter.go)
            └─ extract symbols (parser/extract.go)
                 └─ call OpenRouter → summary + tags  (llm/)
                      └─ build text block (llm/prompts.go)
                           └─ embed text block (embedding/)
                                └─ upsert to Qdrant (store/)
```

**Data flow for `nav search`:**

```
Query string
  └─ embed query (embedding/)
       └─ search Qdrant (store/)
            └─ format results → stdout (or JSON with --json)
```

---

## Directory Layout

```
nav/
├── cmd/
│   └── main.go                    # entry point → cli.Execute()
│
├── internal/
│   ├── cli/
│   │   ├── root.go                # cobra root command, persistent flags
│   │   ├── index.go               # nav index
│   │   ├── search.go              # nav search
│   │   ├── sync.go                # nav sync (reprocess missed commits)
│   │   ├── hook.go                # nav hook install|uninstall|run
│   │   └── config.go              # nav config show|set|init
│   │
│   ├── parser/
│   │   ├── detect.go              # detect language from extension + shebang
│   │   ├── treesitter.go          # tree-sitter query execution per language
│   │   └── extract.go             # map tree-sitter nodes → CodeUnit structs
│   │
│   ├── llm/
│   │   ├── client.go              # OpenRouter HTTP client, retry, rate-limit
│   │   ├── openrouter.go          # model selection, request/response types
│   │   └── prompts.go             # prompt templates + text block builder
│   │
│   ├── embedding/
│   │   ├── client.go              # provider interface
│   │   ├── nvidia.go              # Nemotron Embed VL 1B v2
│   │   ├── qwen.go                # Qwen3 Embedding (0.6B / 8B)
│   │   └── openai.go              # text-embedding-3-small
│   │
│   ├── store/
│   │   ├── qdrant.go              # Qdrant upsert, search, delete
│   │   └── schema.go              # CodeUnit and Payload types
│   │
│   ├── hook/
│   │   ├── git.go                 # install/uninstall/run git pre-commit hook
│   │   └── claude.go              # install/uninstall/run Claude Code hook
│   │
│   └── config/
│       └── config.go              # load/save ~/.nav-cli/config.yaml via viper
│
├── go.mod
├── go.sum
└── README.md
```

---

## Config Directory Layout

`nav` stores all state in `$HOME/.nav-cli/`:

```
~/.nav-cli/
├── config.yaml            # global settings (see below)
├── credentials            # API keys (chmod 600, never in config.yaml)
├── projects/
│   ├── project1.yaml        # per-project overrides (model, collection, paths)
│   ├── project1
│       └── readme.md
│   └── project2.yaml
└── logs/
    └── sync.log           # missed-commit reprocessing log
```

### `config.yaml`

```yaml
qdrant:
  url: http://localhost:6333
  api_key: ""                    # leave empty for local unauthenticated instance

llm:
  provider: openrouter
  model: qwen/qwen3-coder        # default summarisation model
  readme_model: qwen/qwen3-coder # model used to generate the project README
  fallback_models:
    - mistralai/devstral-2
    - meta-llama/llama-3.3-70b-instruct
  request_timeout: 60            # timeout (in seconds) for LLM requests
  readme_timeout: 300            # timeout (in seconds) for README generation

embedding:
  provider: nvidia               # nvidia | qwen | openai
  model: nvidia/nemotron-embed-vl-1b-v2
  dimension: 1024                # must match the Qdrant collection
  request_timeout: 120           # timeout (in seconds) for embedding requests (useful for large projects)

indexing:
  concurrency: 4                 # parallel symbol processing goroutines
  skip_patterns:                 # glob patterns relative to repo root
	- vendor/**
	- node_modules/**
	- **/*_test.go
	- **/*.pb.go
	- dist/**
	- venv/**
	- .venv/**
	- env/**
	- .env/**
	- virtualenv/**
	- **/site-packages/**
	- **/__pycache__/**
  min_lines: 3                   # skip symbols shorter than N lines

hooks:
  git_skip_env: NAV_SKIP         # env var checked by the pre-commit hook
  claude_top_k: 5                # how many results to inject into Claude context
```

### `credentials`

```
OPENROUTER_API_KEY=sk-or-...
NVIDIA_API_KEY=nvapi-...
OPENAI_API_KEY=sk-...
```

Loaded automatically at startup. Never written by `nav config set` — edit by hand or use `nav config set-key`.

### Per-project override (`projects/mokosh.yaml`)

```yaml
project: mokosh
path: ~/work/mokosh
collection: nav_mokosh            # Qdrant collection name; defaults to "nav_<project>"
language_overrides:
  "src/generated/**": skip
embedding:
  provider: openai                # override global embedding for this project
```

---

## Code Unit Schema

Each indexed symbol produces one Qdrant point:

```json
{
  "id": "mokosh_user_service_create_user",
  "vector": [/* dense float32 array, length = embedding dimension */],
  "payload": {
    "project": "mokosh",
    "language": "python",

    "type": "method",
    "symbol": "UserService.create_user",
    "parent": "UserService",

    "file_path": "services/user/service.py",
    "module": "services.user.service",

    "signature": "create_user(email: str, password: str) -> User",

    "content": "async def create_user(self, email: str, password: str) -> User:\n    ...",

    "summary": "Creates a new user, hashes password, stores it, and sends welcome email",

    "tags": ["user", "auth", "creation", "email"],

    "business_context": "Onboards a new customer so they can access the product.",
    "responsibilities": ["validate input", "persist the user", "trigger welcome email"],

    "imports": ["validate_email", "hash_password"],
    "calls": [
      "validate_email",
      "hash_password",
      "repo.create",
      "email_service.send_welcome"
    ],

    "called_by": ["UserController.register"],

    "framework": "fastapi",
    "layer": "service",

    "last_modified": 1710000000
  }
}
```

**Field notes:**

| Field | Source | Notes |
|---|---|---|
| `id` | computed | `<project>_<module_dotpath>_<symbol_snake>` |
| `vector` | embedding provider | dense float32 |
| `type` | tree-sitter | `function`, `method`, `class`, `interface`, `struct`, `constant` |
| `symbol` | tree-sitter | qualified name: `Parent.method` for methods |
| `parent` | tree-sitter | enclosing class/struct; empty for top-level functions |
| `module` | file path → dotpath | language-aware conversion |
| `signature` | tree-sitter | parameter list + return type |
| `content` | file bytes | raw source of the symbol node |
| `summary` | LLM | dense description (up to 200 chars) of what the symbol does |
| `tags` | LLM | 3–6 lowercase keywords |
| `business_context` | LLM | one sentence on the business/domain purpose the symbol serves |
| `responsibilities` | LLM | 1–4 short phrases naming the distinct responsibilities the symbol owns |
| `imports` | tree-sitter | identifiers imported at the file level that appear in the symbol body |
| `calls` | tree-sitter | direct function/method call sites inside the symbol body |
| `called_by` | post-index pass | symbols in the same project that call this one, computed during a full index |
| `framework` | heuristic | inferred from import names |
| `layer` | path heuristic | `controller`, `service`, `repository`, `model`, `middleware`, `util` |
| `last_modified` | `git log` | unix timestamp of the last commit that touched the file |

---

## Text Representation

Before embedding, the payload is serialised into a plain-text block. This is what the embedding model actually receives:

```
Symbol: UserService.create_user
Type: method
File: services/user/service.py
Layer: service
Language: python

Purpose:
Creates a new user, hashes password, stores it, and sends welcome email

Dependencies:
validate_email, hash_password, repo.create, email_service.send_welcome

Tags:
user, auth, creation, email

Code:
async def create_user(self, email: str, password: str) -> User:
    validate_email(email)

    hashed = hash_password(password)
    user = await self.repo.create(email=email, password=hashed)

    await self.email_service.send_welcome(email)

    return user
```

The text block is also stored in `payload.text` so search results can be displayed without reconstruction.

---

## Initialization

```
nav init
```

On first run, bootstraps `~/.nav-cli/` and prompts for:

- Qdrant URL (default: `http://localhost:6333`)
- Qdrant API key (optional)
- Default LLM model
- Default embedding provider + model
- OpenRouter API key
- Embedding provider API key

Re-running `init` is safe — existing values are never overwritten.

---

## Command Reference

### `nav init`

Bootstrap `~/.nav-cli/` config directory. Safe to re-run.

---

### `nav index`

Parse a repository and (re-)index all symbols into Qdrant.

```
nav index --project <name> --path <repo-root> [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--project` | required | logical project name, used as Qdrant collection prefix |
| `--path` | required | absolute or relative path to the repository root |
| `--concurrency` | 4 | parallel goroutines for LLM + embedding calls |
| `--dry-run` | false | parse and extract symbols, skip LLM and Qdrant writes |
| `--force` | false | re-index all symbols even if `last_modified` is unchanged |
| `--lang` | auto | restrict to a single language (`go`, `python`, `typescript`, …) |
| `--collection` | nav_\<project\> | override Qdrant collection name |
| `--ignore-dir` | none | directories to exclude from indexing (can be specified multiple times) |

Full reindex of a project:

```bash
nav index --project mokosh --path ~/work/mokosh
```

Dry-run to inspect what would be indexed:

```bash
nav index --project mokosh --path ~/work/mokosh --dry-run
```

Skip indexing specific directories using --ignore-dir (can be used multiple times):

```bash
nav index --project mokosh --path ~/work/mokosh --ignore-dir vendor --ignore-dir dist
```

---

### `nav search`

Search indexed symbols by semantic similarity.

```
nav search --project <name> <query> [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--project` | required | project to search |
| `--top` | 5 | number of results to return |
| `--type` | all | filter by symbol type: `function`, `method`, `class`, … |
| `--lang` | all | filter by language |
| `--layer` | all | filter by layer: `service`, `controller`, … |
| `--json` | false | output results as JSON instead of human-readable text |
| `--threshold` | 0.70 | minimum cosine similarity score |

```bash
nav search --project mokosh "password hashing"
nav search --project mokosh "email delivery" --type method --top 3
nav search --project mokosh "database connection pool" --json
```

---

### `nav sync`

Re-process commits whose changed files were not indexed (e.g. when `NAV_SKIP=1` was set, or the hook was not yet installed).

```
nav sync --project <name> --path <repo-root> [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--since` | last sync | git revision or ISO date from which to replay |
| `--dry-run` | false | show which commits would be processed |

```bash
nav sync --project mokosh --path ~/work/mokosh --since 2024-01-01
nav sync --project mokosh --path ~/work/mokosh --since HEAD~10
```

`sync` reads the git log, identifies files changed in each commit, re-extracts symbols from those files, and upserts into Qdrant. It is idempotent — re-running is always safe.

---

### `nav hook`

Manage git and Claude Code hook installation.

```
nav hook install   --type git    --project <name> --path <repo-root>
nav hook install   --type claude --project <name>
nav hook uninstall --type git    --path <repo-root>
nav hook uninstall --type claude --project <name>
nav hook run       --type git    --path <repo-root>   # called by the hook itself
nav hook run       --type claude --query <text>        # called by the Claude hook
```

---

### `nav config`

Inspect and modify global configuration.

```
nav config show
nav config set  <key> <value>
nav config set-key <provider> <api-key>   # writes to credentials, not config.yaml
```

---

## Language Support

Languages are detected by file extension and optional shebang inspection. Tree-sitter grammars are embedded as CGo bindings via `github.com/smacker/go-tree-sitter`.

| Language | Extensions | Symbol types extracted |
|---|---|---|
| Go | `.go` | function, method, struct, interface, constant |
| Python | `.py` | function, method, class |
| TypeScript | `.ts`, `.tsx` | function, method, class, interface, arrow function |
| JavaScript | `.js`, `.jsx` | function, method, class, arrow function |
| Rust | `.rs` | function, method, struct, enum, trait impl |
| Java | `.java` | method, class, interface |
| C / C++ | `.c`, `.cpp`, `.h` | function, struct |
| Ruby | `.rb` | method, class, module |

Additional languages can be added by dropping a tree-sitter grammar binding and a query file into `internal/parser/queries/<lang>.scm`.

### Tree-sitter query files

Each language ships a `.scm` query that captures named symbols. Example for Go (`queries/go.scm`):

```scheme
(function_declaration
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
  result: (_)? @symbol.return
  body: (block) @symbol.body) @symbol.definition

(method_declaration
  receiver: (parameter_list) @symbol.receiver
  name: (field_identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
  result: (_)? @symbol.return
  body: (block) @symbol.body) @symbol.definition
```

---

## LLM Providers (OpenRouter)

`nav` calls OpenRouter to enrich each symbol — generating the `summary`, `tags`, `business_context` and `responsibilities` fields — and once per full index to write the project README (see [Project README](#project-readme)). All structural fields come from tree-sitter.

**Default model priority:**

1. `qwen/qwen3-coder` — primary; best code understanding
2. `mistralai/devstral-2` — first fallback
3. `meta-llama/llama-3.3-70b-instruct` — second fallback

Models are tried in order on rate-limit or error. All three are free-tier models on OpenRouter.

**Prompt contract** (see `internal/llm/prompts.go`):

```
You are a code documentation assistant.
Given the source code below, respond with a JSON object containing:
  "summary": dense description of what this symbol does, up to 200 characters
  "tags": array of 3-6 lowercase keywords
  "businessContext": one sentence on the business/domain purpose this code serves
  "responsibilities": array of 1-4 short phrases naming the responsibilities it owns

Language: {language}
Symbol: {symbol}
Type: {type}

Source:
{content}
```

Batch size is configurable (`llm.batch_size` in `config.yaml`, default 10). Requests within a batch are fired concurrently with the configured `concurrency`.

---

## Project README

After a **full** `nav index` (not an incremental `nav sync`), `nav` makes one additional OpenRouter call to generate a business-oriented README for the project and writes it to:

```
~/.nav-cli/projects/<project>/readme.md
```

This document deliberately contains **no code, signatures or file paths**. It synthesises the per-symbol `business_context` notes into a high-level description of what the project is for, the domain problems it solves and the workflows it supports, plus a short note on the technical stack and notable architecture decisions. README generation is best-effort: a failure is logged as a warning and never aborts indexing, since the symbols are already stored in Qdrant.

The model is configurable via `llm.readme_model` in `config.yaml` (default `qwen/qwen3-coder`); on failure it falls back to the configured `llm.fallback_models`.

---

## Embedding Providers

The text block (see [Text Representation](#text-representation)) is sent to the configured embedding provider. Only one provider is active at a time per project — mixing providers requires a full reindex because vector spaces are incompatible.

| Provider | Model | Dimension | Notes |
|---|---|---|---|
| `nvidia` | `nvidia/nemotron-embed-vl-1b-v2` | 1024 | Default; best code retrieval quality |
| `qwen` | `qwen/qwen3-embedding-0.6b` | 1024 | Lightweight, fast |
| `qwen` | `qwen/qwen3-embedding-8b` | 4096 | High accuracy, higher cost |
| `openai` | `text-embedding-3-small` | 1536 | Widely supported fallback |

All providers are accessed through OpenRouter's unified embedding endpoint where available, or their native API otherwise.

**Changing the embedding model** for an existing project requires a full reindex (`nav index --force`) because all vectors must share the same space.

---

## Qdrant Integration

`nav` uses Qdrant as its only persistence layer. Each project maps to one Qdrant collection named `nav_<project>` by default.

### Collection schema

Collections are created automatically on first index with the dimension derived from the configured embedding model:

```json
{
  "vectors": {
    "size": 1024,
    "distance": "Cosine"
  }
}
```

### Payload indices

`nav` creates payload indices on first run to enable filtered searches:

```
type, language, layer, file_path, project, last_modified
```

### Search with filters

```bash
nav search --project mokosh "authentication" --type method --layer service
```

Translates to a Qdrant filtered search:

```json
{
  "filter": {
    "must": [
      {"key": "type",    "match": {"value": "method"}},
      {"key": "layer",   "match": {"value": "service"}},
      {"key": "project", "match": {"value": "mokosh"}}
    ]
  },
  "limit": 5,
  "with_payload": true
}
```

### Qdrant setup (local)

```bash
docker run -d --name qdrant \
  -p 6333:6333 \
  -v ~/.nav-cli/qdrant_storage:/qdrant/storage \
  qdrant/qdrant
```

Or point `qdrant.url` at any Qdrant Cloud instance.

---

## Git Hook Integration

The git pre-commit hook keeps the Qdrant index in sync with every commit automatically.

### How it works

1. `git commit` triggers `.git/hooks/pre-commit`.
2. The hook calls `nav hook run --type git --path .`.
3. `nav` reads `git diff --cached --name-only` to get the list of staged files.
4. For each changed file, it re-parses symbols and upserts updated points into Qdrant.
5. Symbols from deleted files are removed from Qdrant.
6. The hook exits 0 — it never blocks the commit.

### Installation

```bash
nav hook install --type git --project mokosh --path ~/work/mokosh
```

This writes `.git/hooks/pre-commit` in the target repository:

```bash
#!/usr/bin/env bash
[ -n "$NAV_SKIP" ] && exit 0
nav hook run --type git --path "$(git rev-parse --show-toplevel)"
```

The hook is installed per-repository and does not affect other repos.

### Skipping the hook

The hook respects the `NAV_SKIP` environment variable (configurable via `hooks.git_skip_env`):

```bash
NAV_SKIP=1 git commit -m "wip: scratch work"
```

This is the equivalent of `--no-verify` for nav. Commits made with `NAV_SKIP=1` can be reprocessed later with `nav sync`.

### Replaying skipped commits

```bash
nav sync --project mokosh --path ~/work/mokosh --since HEAD~5
```

`sync` replays any commits that the hook did not process by walking the git log and upserting symbols from changed files.

### Uninstallation

```bash
nav hook uninstall --type git --path ~/work/mokosh
```

Removes the `.git/hooks/pre-commit` file. Does not touch the Qdrant index.

---

## Claude Code Integration

The Claude Code hook injects semantically relevant code units into every AI session, giving Claude context about the current project before it reads a single source file.

### How it works

1. The user sends a message to Claude Code inside a project.
2. The `PreToolUse` or `UserPromptSubmit` Claude Code hook fires and calls `nav hook run --type claude --query "<user message>"`.
3. `nav` embeds the query, searches Qdrant for the top-K most relevant symbols, and writes a context block to stdout.
4. Claude Code injects that block into the conversation context before processing the user's request.

### Installation

```bash
nav hook install --type claude --project mokosh
```

This writes the hook entry into `.claude/settings.json` in the current working directory (or globally to `~/.claude/settings.json` if `--global` is passed):

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "nav hook run --type claude --project mokosh --top 5 --query \"$CLAUDE_USER_PROMPT\""
          }
        ]
      }
    ]
  }
}
```

### Output format (injected into context)

```
<nav-context project="mokosh" query="password hashing and user creation">

--- Result 1 (score: 0.94) ---
Symbol: UserService.create_user
Type: method
File: services/user/service.py
Layer: service

Purpose:
Creates a new user, hashes password, stores it, and sends welcome email

Code:
async def create_user(self, email: str, password: str) -> User:
    ...

--- Result 2 (score: 0.89) ---
...

</nav-context>
```

The `<nav-context>` block is prepended to the conversation turn so Claude Code sees it before responding.

### Controlling context injection

```yaml
# ~/.nav-cli/config.yaml
hooks:
  claude_top_k: 5           # number of results injected
  claude_min_score: 0.72    # minimum similarity score; lower results are dropped
  claude_max_tokens: 4000   # hard cap on total injected text length
```

### Uninstallation

```bash
nav hook uninstall --type claude --project mokosh
```

Removes the hook entry from `.claude/settings.json`. Does not touch the Qdrant index.

---

## Development

### Prerequisites

- Go 1.22+
- CGo enabled (required by `go-tree-sitter`)
- A running Qdrant instance (see [Qdrant setup](#qdrant-setup-local))
- An OpenRouter API key

### Build

```bash
git clone https://github.com/your-org/nav
cd nav
go build -o nav ./cmd
```

### First run

```bash
./nav init
./nav index --project myproject --path ~/work/myproject --dry-run
./nav index --project myproject --path ~/work/myproject
./nav search --project myproject "http request handling"
```

### Key dependencies

| Package | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/viper` | Config loading |
| `github.com/smacker/go-tree-sitter` | Tree-sitter Go bindings |
| `github.com/qdrant/go-client` | Qdrant gRPC client |
| `gopkg.in/yaml.v3` | YAML config serialisation |

### Adding a new language

1. Add a tree-sitter grammar binding to `go.mod` (e.g. `github.com/smacker/go-tree-sitter/python`).
2. Create `internal/parser/queries/<lang>.scm` with capture patterns for the symbol types you want.
3. Register the language in `internal/parser/detect.go` (extension → language handle mapping).
4. Add the language to the extraction switch in `internal/parser/extract.go`.
5. Add a test fixture under `internal/parser/testdata/<lang>/`.

### Adding a new embedding provider

1. Create `internal/embedding/<provider>.go` implementing the `Embedder` interface:
   ```go
   type Embedder interface {
       Embed(ctx context.Context, texts []string) ([][]float32, error)
       Dimension() int
   }
   ```
2. Register the provider in `internal/embedding/client.go`.
3. Document the model + dimension in this README and in `config.yaml`.
