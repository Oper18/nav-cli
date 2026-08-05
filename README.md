# nav

CLI tool for parsing source code repositories into semantically rich, searchable code units — and keeping them fresh as the codebase evolves.

`nav` slices a repository into functions, methods, and classes using tree-sitter, enriches each unit with an LLM-generated summary via OpenRouter, converts the result into vector embeddings, and stores everything in Qdrant. Two integration points keep the index alive: git hooks (pre-commit and every flavor of pull — never push, which doesn't change what's on disk) that patch changed symbols as they happen, and a Claude Code hook that injects relevant code context into every AI-assisted session.

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
│   │   ├── index.go               # nav index (also: embedAndUpsertSymbols, shared with lazysync.go)
│   │   ├── search.go              # nav search
│   │   ├── sync.go                # nav sync (lazy path by default; --since replays missed commits)
│   │   ├── lazysync.go            # lazy re-embedding: change detection, manifest diff, graph rebuild
│   │   ├── graph.go                # nav graph summary|callers|deps|symbol
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
│   ├── db/
│   │   ├── db.go                  # domain client, delegates to qdrant
│   │   ├── qdrant/qdrant.go       # Qdrant upsert, search, delete, ID/payload encoding
│   │   ├── sqlite.go              # per-branch SQLite state: open/migrate/reset (modernc.org/sqlite + morph), meta table
│   │   ├── lock.go                # flock-based single-writer lock (.nav/lock)
│   │   ├── chunks.go              # manifest CRUD (content_hash/embedded_hash per chunk)
│   │   ├── graph.go               # nodes/edges CRUD + queries (callers, deps, fan-in)
│   │   └── migrations/            # embedded SQL schema migrations
│   │
│   ├── hook/
│   │   ├── git.go                 # install/uninstall/run git pre-commit/post-merge/post-rewrite/reference-transaction hooks
│   │   └── claude.go              # install/uninstall/run Claude Code hooks (prompt + session start)
│   │
│   └── config/
│       └── config.go              # load/save ~/.nav-cli/config.yaml via viper
│
├── go.mod
├── go.sum
└── README.md
```

Per-project state lives under `<repo-root>/.nav/` (gitignored): each branch
gets its own `nav-<branch>.db`, holding that branch's chunk manifest and
knowledge graph — what files and symbols exist can differ meaningfully
between branches, so they're never shared. `lock` is the single-writer
flock `nav sync` takes so overlapping hook invocations don't race (shared
across branches, since only one is checked out in a given working tree at a
time).

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

With no flags, `nav sync` is the fast, idempotent lazy re-embedding path — the
same thing the `UserPromptSubmit` hook runs in-process before every prompt.
It keeps a per-branch SQLite manifest at `.nav/nav-<branch>.db` (content hash
per embedded chunk); each run detects files changed since the last sync (via
`git status` and any HEAD movement, or file mtimes outside a git repo),
re-parses only those, and re-embeds only the chunks whose content hash
actually changed. With nothing dirty it's a near no-op (one `git status`
call). It prints a one-line summary:

```
synced: 3 chunks re-embedded, 0 removed
```

```
nav sync [project] [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--path` | project's registered path, or cwd | repository root |
| `--since` | (unset) | switches to commit-log replay mode instead (see below) |
| `--dry-run` | false | show what would change without doing it |

`--since` switches to the older commit-log replay mode: it walks `git log`
for commits after the given date (or ref) and unconditionally re-indexes
every file they touched, ignoring the manifest. This is for catching up
commits made with `NAV_SKIP=1` set, or before the git hook was installed —
not routine use.

```bash
nav sync                                          # lazy path (same as the hook)
nav sync mokosh --path ~/work/mokosh --since 2024-01-01
nav sync mokosh --path ~/work/mokosh --since HEAD~10
```

Both modes are idempotent — re-running is always safe.

---

### `nav graph`

Plain-text, LLM-oriented queries against the knowledge graph `nav sync`
builds alongside the manifest in the same per-branch `.nav/nav-<branch>.db`:
packages, files, and symbols (func/method/type/const) as nodes, and
`defines`/`imports`/`calls`/`implements`/`embeds` relationships as edges.
The graph reflects whatever the *current* branch's code looks like — switch
branches and `nav graph`/`nav sync` follow along, since each branch has its
own file. Reads only from SQLite — no re-parsing, no network calls.

```
nav graph summary [project] [--path <repo-root>]
nav graph callers <symbol> [project] [--depth N]     # default depth 1
nav graph deps <package|file> [project] [--depth N]  # default depth 3
nav graph symbol <name> [project]
```

`nav graph summary` renders a ~1000-token digest — packages with a one-line
responsibility (reusing each package's already-summarised symbols, no new
LLM calls), entry points, and the top-10 most-called symbols by fan-in — and
caches it, regenerating only when the graph has actually changed since the
digest was last built. It's what gets injected on Claude Code's
`SessionStart` hook (see below).

---

### `nav hook`

Manage git, Claude Code, Qwen Code, Cursor, and OpenCode hook installation.

```
nav hook install   [project] --type git    --path <repo-root>
nav hook install   [project] --type claude
nav hook uninstall          --type git    --path <repo-root>
nav hook uninstall          --type claude
nav hook run        [project] --type git                   --path <repo-root>   # called by the git hook itself
nav hook run        [project] --type claude                --query-stdin        # called on UserPromptSubmit (query piped in via jq, see below)
nav hook run        [project] --type claude-session-start                        # called on SessionStart
```

Installing the `claude` hook type registers both a `UserPromptSubmit` entry
(embeds the query, searches Qdrant, injects a `<nav-context>` block — and
first runs `nav sync`'s lazy path in-process so results reflect any
just-made edits) and a `SessionStart` entry (injects `nav graph summary`'s
digest, so a new session starts already oriented in the codebase).

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

nav installs four git hooks that keep the Qdrant index and local knowledge
graph in sync automatically, on every commit *and* every way of pulling
changes in — plain, `--ff-only` (the common case when you're just catching
up), true merge, or `--rebase`. There is deliberately no push hook: a push
doesn't change anything on disk, so there is nothing for nav to (re-)index.

| Hook | Fires on | Action |
|---|---|---|
| `pre-commit` | `git commit` | re-parses and upserts only the staged files |
| `post-merge` | `git merge` producing an actual merge commit (and therefore `git pull` when it isn't a fast-forward) | runs the lazy sync (`nav sync`) |
| `post-rewrite` | `git rebase` (reason `rebase`), and therefore `git pull --rebase` when it actually replays commits | runs the lazy sync (`nav sync`) |
| `reference-transaction` | any update to the checked-out branch's own ref | runs the lazy sync (`nav sync`) |

Every one of these hooks funnels into the same lazy sync (`services.LazySync`),
which is what makes this cheap and correct: it diffs the working tree and,
when `HEAD` has moved since the last sync, the commit range too, then
re-parses only the files that touched and re-embeds only the symbols whose
content hash actually changed. A chunk whose hash matches what the manifest
already has is left alone. On the pull side, `post-merge`/`post-rewrite`/
`reference-transaction` diff against the last synced `HEAD`, so every object
touched by the incoming commits gets revalidated and, where it's actually
dirty, re-embedded and written back to both Qdrant and the local SQLite
state — never silently missed just because the pull happened to be a
fast-forward.

`post-merge` and `post-rewrite` sound like they should cover every `git
pull`, but they don't: git skips its merge/rebase machinery entirely for a
pure fast-forward — the single most common pull of all, when you have no
local commits of your own — so neither hook fires for it.
`reference-transaction` is what actually closes that gap: it fires after
*any* ref update, fast-forward included, so between the three pull-side
hooks, every flavor of `git pull` really does end up triggering a sync. It's
filtered to the checked-out branch's own ref (or `HEAD`) so a plain `git
fetch` — which only moves remote-tracking refs — stays quiet, and it also
means switching branches (`git checkout`/`git switch`) proactively syncs the
branch you just moved to, which fits naturally with the graph being
per-branch (see [Directory Layout](#directory-layout)).

### How it works

1. `git commit` triggers `.git/hooks/pre-commit`, which calls
   `nav hook run --type git --path .`: `nav` reads
   `git diff --cached --name-only` for the staged files, re-parses and
   upserts them into Qdrant, and removes symbols from deleted files. It
   exits 0 either way — it never blocks the commit.
2. A real `git merge` (a merge commit, not a fast-forward) triggers
   `.git/hooks/post-merge`, which calls
   `nav hook run --type git-post-merge --path .`: this runs the same lazy
   sync `nav sync` does, detecting every file that changed since the last
   sync (via the commit range, not just the merge itself), revalidating each
   one against the manifest, and re-embedding only what's dirty.
3. An actual `git rebase` replay triggers `.git/hooks/post-rewrite` with
   `$1=rebase`; the hook forwards to the same `git-post-merge` run type as
   above. `git commit --amend` also fires `post-rewrite`, but with
   `$1=amend`, which the hook ignores.
4. Any update to the checked-out branch's own ref (or `HEAD`) — a
   fast-forward pull, a merge, a rebase, a commit, a branch switch — fires
   `.git/hooks/reference-transaction` with a `committed` transaction state
   and the updated ref on stdin; the hook checks the ref against
   `refs/heads/$(git rev-parse --abbrev-ref HEAD)` (or a literal `HEAD`)
   before forwarding to `git-post-merge` too, so it ignores `git fetch`
   updating only `refs/remotes/...`.

`git push` triggers no hook at all — a push only moves a remote ref, it never
changes a file on the machine running it, so there is nothing for nav to
re-index.

### Installation

```bash
nav hook install --type git --project mokosh --path ~/work/mokosh
```

This writes `.git/hooks/pre-commit`, `.git/hooks/post-merge`,
`.git/hooks/post-rewrite`, and `.git/hooks/reference-transaction` in the
target repository:

```bash
#!/usr/bin/env bash
# pre-commit
[ -n "$NAV_SKIP" ] && exit 0
nav hook run --type git --path "$(git rev-parse --show-toplevel)"
```

```bash
#!/usr/bin/env bash
# post-merge
nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
```

```bash
#!/usr/bin/env bash
# post-rewrite
[ "$1" = "rebase" ] || exit 0
nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
```

```bash
#!/usr/bin/env bash
# reference-transaction
[ "$1" = "committed" ] || exit 0
branch="refs/heads/$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
while read -r old new ref; do
  if [ "$ref" = "$branch" ] || [ "$ref" = "HEAD" ]; then
    nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
    exit 0
  fi
done
```

Each hook is installed per-repository and does not affect other repos. If a
hook file already exists and isn't nav's, nav appends its call rather than
overwriting the file.

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

Removes the nav portion of `.git/hooks/pre-commit`, `post-merge`,
`post-rewrite`, and `reference-transaction` (the whole file if nav owned it
outright, or just nav's appended lines if it was layered onto an existing
hook), and cleans up a `pre-push` hook left behind by an older nav version
(current nav doesn't install one). Does not touch the Qdrant index.

---

## Claude Code Integration

The Claude Code hook injects semantically relevant code units into every AI session, giving Claude context about the current project before it reads a single source file.

### How it works

**Session start:** the `SessionStart` hook fires once per session and calls
`nav hook run --type claude-session-start`, which prints `nav graph
summary`'s cached digest — packages, entry points, top-called symbols — so
Claude starts oriented in the codebase before it reads a single file.

**Every prompt:**

1. The user sends a message to Claude Code inside a project.
2. The `UserPromptSubmit` hook fires and calls `jq -r '.prompt' | nav hook run --type claude --query-stdin` — Claude Code passes the prompt as the `"prompt"` field of a JSON payload on stdin, not as an env var, so `jq` pulls it out and `--query-stdin` reads the result. (An earlier version of this hook referenced a `$CLAUDE_USER_PROMPT` env var that Claude Code never actually sets, so it silently never matched anything — if your installed hook still has that in it, reinstall with `nav hook install --type claude`.)
3. `nav` first runs the lazy sync path in-process (§ `nav sync`) so the index reflects any edits made since the last prompt — a near no-op when nothing changed.
4. `nav` embeds the query, searches Qdrant for the top-K most relevant symbols, and writes a context block to stdout.
5. Claude Code injects that block into the conversation context before processing the user's request.

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
            "command": "jq -r '.prompt' | nav hook run mokosh --type claude --top 5 --query-stdin"
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
| `modernc.org/sqlite` | Pure-Go SQLite driver for `.nav/nav-<branch>.db` (no cgo) |
| `github.com/mattermost/morph` | SQLite schema migrations for `internal/db` |
| `github.com/cespare/xxhash/v2` | Content hashing for the chunk manifest |
| `golang.org/x/sys` | `flock` for the single-writer sync lock |

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
