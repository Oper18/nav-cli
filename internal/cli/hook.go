package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"nav/config"
	"nav/internal/db"
	"nav/internal/hook"
	"nav/internal/llm"
	"nav/internal/services"
)

// ---------------------------------------------------------------------------
// Top-level hook command
// ---------------------------------------------------------------------------

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Manage git, Claude Code, Qwen Code, Cursor, and OpenCode hook installation",
}

// ---------------------------------------------------------------------------
// hook install
// ---------------------------------------------------------------------------

var (
	hookInstallType   string
	hookInstallPath   string
	hookInstallGlobal bool
)

var hookInstallCmd = &cobra.Command{
	Use:   "install [project]",
	Short: "Install a nav hook (git pre-commit, Claude Code, Qwen Code, Cursor, or OpenCode)",
	Long: "Install a nav hook (git pre-commit, Claude Code, Qwen Code, Cursor, or OpenCode).\n\n" +
		"The project name is optional: when omitted, the current directory must be a\n" +
		"git repository and its basename is used as the project name.",
	Args: cobra.MaximumNArgs(1),
	RunE: runHookInstall,
}

func init() {
	hookInstallCmd.Flags().StringVar(&hookInstallType, "type", "", `Hook type: "git", "claude", "qwen", "cursor", or "opencode" (required)`)
	hookInstallCmd.Flags().StringVar(&hookInstallPath, "path", ".", "Repository path (for git hooks)")
	hookInstallCmd.Flags().BoolVar(&hookInstallGlobal, "global", false, "Use ~/.claude/settings.json instead of ./.claude/settings.json")

	_ = hookInstallCmd.MarkFlagRequired("type")
}

func runHookInstall(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	switch hookInstallType {
	case "git":
		path := hookInstallPath
		if path == "" {
			path = "."
		}
		if err := hook.Install(path, cfg); err != nil {
			return fmt.Errorf("installing git hook: %w", err)
		}
		fmt.Printf("nav git hook installed in %s/.git/hooks/pre-commit\n", path)

	case "claude":
		project, _, err := resolveProject(args, hookInstallPath)
		if err != nil {
			return err
		}
		var settingsPath string
		if hookInstallGlobal {
			settingsPath = hook.GlobalSettingsPath()
		} else {
			dir := hookInstallPath
			if dir == "" {
				dir = "."
			}
			settingsPath = hook.DefaultSettingsPath(dir)
		}
		topK := cfg.Hooks.ClaudeTopK
		if err := hook.InstallClaude(settingsPath, project, topK); err != nil {
			return fmt.Errorf("installing Claude hook: %w", err)
		}
		fmt.Printf("nav Claude hook installed in %s\n", settingsPath)

	case "qwen":
		project, _, err := resolveProject(args, hookInstallPath)
		if err != nil {
			return err
		}
		var settingsPath string
		if hookInstallGlobal {
			settingsPath = hook.QwenGlobalSettingsPath()
		} else {
			dir := hookInstallPath
			if dir == "" {
				dir = "."
			}
			settingsPath = hook.QwenDefaultSettingsPath(dir)
		}
		topK := cfg.Hooks.QwenTopK // Use Qwen-specific configuration
		if err := hook.InstallQwen(settingsPath, project, topK); err != nil {
			return fmt.Errorf("installing Qwen hook: %w", err)
		}
		fmt.Printf("nav Qwen hook installed in %s\n", settingsPath)

	case "cursor":
		project, _, err := resolveProject(args, hookInstallPath)
		if err != nil {
			return err
		}
		var settingsPath string
		if hookInstallGlobal {
			settingsPath = hook.CursorGlobalSettingsPath()
		} else {
			dir := hookInstallPath
			if dir == "" {
				dir = "."
			}
			settingsPath = hook.CursorDefaultSettingsPath(dir)
		}
		topK := cfg.Hooks.CursorTopK
		if err := hook.InstallCursor(settingsPath, project, topK); err != nil {
			return fmt.Errorf("installing Cursor hook: %w", err)
		}
		fmt.Printf("nav Cursor hook installed in %s\n", settingsPath)

	case "opencode":
		project, _, err := resolveProject(args, hookInstallPath)
		if err != nil {
			return err
		}
		dir := hookInstallPath
		if dir == "" {
			dir = "."
		}
		topK := cfg.Hooks.OpenCodeTopK
		if err := hook.InstallOpenCode(dir, project, topK); err != nil {
			return fmt.Errorf("installing OpenCode hook: %w", err)
		}
		fmt.Printf("nav OpenCode hook installed in %s/.opencode/plugins/nav-hook.js\n", dir)

	default:
		return fmt.Errorf("unknown hook type %q; must be \"git\", \"claude\", \"qwen\", \"cursor\", or \"opencode\"", hookInstallType)
	}
	return nil
}

// ---------------------------------------------------------------------------
// hook uninstall
// ---------------------------------------------------------------------------

var (
	hookUninstallType   string
	hookUninstallPath   string
	hookUninstallGlobal bool
)

var hookUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove a nav hook",
	RunE:  runHookUninstall,
}

func init() {
	hookUninstallCmd.Flags().StringVar(&hookUninstallType, "type", "", `Hook type: "git", "claude", "qwen", "cursor", or "opencode" (required)`)
	hookUninstallCmd.Flags().StringVar(&hookUninstallPath, "path", ".", "Repository / settings path")
	hookUninstallCmd.Flags().BoolVar(&hookUninstallGlobal, "global", false, "Use ~/.claude/settings.json")

	_ = hookUninstallCmd.MarkFlagRequired("type")
}

func runHookUninstall(cmd *cobra.Command, args []string) error {
	switch hookUninstallType {
	case "git":
		path := hookUninstallPath
		if path == "" {
			path = "."
		}
		if err := hook.Uninstall(path); err != nil {
			return fmt.Errorf("uninstalling git hook: %w", err)
		}
		fmt.Printf("nav git hook removed from %s\n", path)

	case "claude":
		var settingsPath string
		if hookUninstallGlobal {
			settingsPath = hook.GlobalSettingsPath()
		} else {
			dir := hookUninstallPath
			if dir == "" {
				dir = "."
			}
			settingsPath = hook.DefaultSettingsPath(dir)
		}
		if err := hook.UninstallClaude(settingsPath); err != nil {
			return fmt.Errorf("uninstalling Claude hook: %w", err)
		}
		fmt.Printf("nav Claude hook removed from %s\n", settingsPath)

	case "qwen":
		var settingsPath string
		if hookUninstallGlobal {
			settingsPath = hook.QwenGlobalSettingsPath()
		} else {
			dir := hookUninstallPath
			if dir == "" {
				dir = "."
			}
			settingsPath = hook.QwenDefaultSettingsPath(dir)
		}
		if err := hook.UninstallQwen(settingsPath); err != nil {
			return fmt.Errorf("uninstalling Qwen hook: %w", err)
		}
		fmt.Printf("nav Qwen hook removed from %s\n", settingsPath)

	case "cursor":
		var settingsPath string
		if hookUninstallGlobal {
			settingsPath = hook.CursorGlobalSettingsPath()
		} else {
			dir := hookUninstallPath
			if dir == "" {
				dir = "."
			}
			settingsPath = hook.CursorDefaultSettingsPath(dir)
		}
		if err := hook.UninstallCursor(settingsPath); err != nil {
			return fmt.Errorf("uninstalling Cursor hook: %w", err)
		}
		fmt.Printf("nav Cursor hook removed from %s\n", settingsPath)

	case "opencode":
		dir := hookUninstallPath
		if dir == "" {
			dir = "."
		}
		if err := hook.UninstallOpenCode(dir); err != nil {
			return fmt.Errorf("uninstalling OpenCode hook: %w", err)
		}
		fmt.Printf("nav OpenCode hook removed from %s/.opencode/plugins/nav-hook.js\n", dir)

	default:
		return fmt.Errorf("unknown hook type %q; must be \"git\", \"claude\", \"qwen\", \"cursor\", or \"opencode\"", hookUninstallType)
	}
	return nil
}

// ---------------------------------------------------------------------------
// hook run
// ---------------------------------------------------------------------------

var (
	hookRunType  string
	hookRunPath  string
	hookRunTop   int
	hookRunQuery string
)

var hookRunCmd = &cobra.Command{
	Use:   "run [project]",
	Short: "Execute hook logic (called by the hook scripts themselves)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runHookRun,
}

func init() {
	hookRunCmd.Flags().StringVar(&hookRunType, "type", "", `Hook type: "git", "claude", "qwen", "cursor", or "opencode" (required)`)
	hookRunCmd.Flags().StringVar(&hookRunPath, "path", ".", "Repository path (for git hooks)")
	hookRunCmd.Flags().IntVar(&hookRunTop, "top", 5, "Number of results to inject (for claude, qwen, cursor, and opencode hooks)")
	hookRunCmd.Flags().StringVar(&hookRunQuery, "query", "", "Query text (for claude, qwen, cursor, and opencode hooks)")

	_ = hookRunCmd.MarkFlagRequired("type")
}

func runHookRun(cmd *cobra.Command, args []string) error {
	switch hookRunType {
	case "git":
		return runHookRunGit(hookRunPath)

	case "claude":
		project, _, err := resolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunClaude(project, hookRunQuery, hookRunTop)

	case "qwen":
		project, _, err := resolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunQwen(project, hookRunQuery, hookRunTop) // Call dedicated Qwen function

	case "cursor":
		project, _, err := resolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunCursor(project, hookRunQuery, hookRunTop) // Call dedicated Cursor function

	case "opencode":
		project, _, err := resolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunOpenCode(project, hookRunQuery, hookRunTop) // Call dedicated OpenCode function

	default:
		return fmt.Errorf("unknown hook type %q; must be \"git\", \"claude\", \"qwen\", \"cursor\", or \"opencode\"", hookRunType)
	}
}

// runHookRunGit handles the git pre-commit hook: re-indexes changed files and
// removes deleted files from Qdrant.
func runHookRunGit(repoPath string) error {
	changed, deleted, err := hook.StagedFiles(repoPath)
	if err != nil {
		return fmt.Errorf("reading staged files: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	// Derive a project name from the directory basename as a sensible default.
	// The hook script can be customised to pass --project explicitly.
	project := "default"

	ctx := context.Background()

	if len(changed) > 0 {
		if err := indexSpecificFiles(ctx, project, repoPath, "", "", cfg.Indexing.Concurrency, false, changed, []string{}); err != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: re-indexing: %v\n", err)
		}
	}

	if len(deleted) > 0 {
		collection := "nav_" + project
		if qErr := services.EnsureLocalQdrant(cfg); qErr != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: ensuring local qdrant: %v\n", qErr)
		}
		qdrantClient, qErr := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
		if qErr != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: creating qdrant client: %v\n", qErr)
		} else {
			defer qdrantClient.Close()
			branch := currentBranch(repoPath)
			ids, qErr := deletedFileIDs(ctx, qdrantClient, collection, branch, deleted)
			if qErr != nil {
				fmt.Fprintf(os.Stderr, "nav: warn: querying deleted files: %v\n", qErr)
			}
			if len(ids) > 0 {
				if dErr := qdrantClient.Delete(ctx, collection, ids); dErr != nil {
					fmt.Fprintf(os.Stderr, "nav: warn: deleting symbols: %v\n", dErr)
				}
			}
		}
	}

	fmt.Printf("nav: updated %d symbols\n", len(changed))
	return nil
}

// runHookRunClaude handles the Claude Code prompt hook: embeds the query,
// searches Qdrant, formats a context block, and prints it.
func runHookRunClaude(project, query string, topK int) error {
	if query == "" {
		return nil // nothing to do
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
		time.Duration(cfg.LLM.RequestTimeout)*time.Second, time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

	ctx := context.Background()

	vecs, err := llmClient.EmbedQuery(ctx, cfg.Embedding.Model, cfg.Embedding.QueryInstruction, []string{query})
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) == 0 {
		return fmt.Errorf("embedder returned no vectors")
	}

	collection := "nav_" + project
	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	results, err := qdrantClient.Search(ctx, collection, vecs[0], overFetch(topK), cfg.Hooks.ClaudeMinScore, nil)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}
	results = topN(collapseChunks(results), topK)

	// Convert to hook.ContextResult.
	ctxResults := make([]hook.ContextResult, 0, len(results))
	for _, r := range results {
		ctxResults = append(ctxResults, hook.ContextResult{
			Score:   r.Score,
			Symbol:  r.Payload.Symbol,
			Type:    r.Payload.Type,
			File:    r.Payload.FilePath,
			Purpose: r.Payload.Summary,
			Code:    r.Payload.Content,
		})
	}

	block := hook.FormatContextBlock(project, query, ctxResults, cfg.Hooks.ClaudeMaxTokens)
	fmt.Println(block)
	return nil
}

// runHookRunQwen handles the Qwen Code prompt hook: embeds the query,
// searches Qdrant, formats a context block, and prints it.
func runHookRunQwen(project, query string, topK int) error {
	if query == "" {
		return nil // nothing to do
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
		time.Duration(cfg.LLM.RequestTimeout)*time.Second, time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

	ctx := context.Background()

	vecs, err := llmClient.EmbedQuery(ctx, cfg.Embedding.Model, cfg.Embedding.QueryInstruction, []string{query})
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) == 0 {
		return fmt.Errorf("embedder returned no vectors")
	}

	collection := "nav_" + project
	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	results, err := qdrantClient.Search(ctx, collection, vecs[0], overFetch(topK), cfg.Hooks.QwenMinScore, nil)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}
	results = topN(collapseChunks(results), topK)

	// Convert to hook.ContextResult.
	ctxResults := make([]hook.ContextResult, 0, len(results))
	for _, r := range results {
		ctxResults = append(ctxResults, hook.ContextResult{
			Score:   r.Score,
			Symbol:  r.Payload.Symbol,
			Type:    r.Payload.Type,
			File:    r.Payload.FilePath,
			Purpose: r.Payload.Summary,
			Code:    r.Payload.Content,
		})
	}

	block := hook.FormatContextBlock(project, query, ctxResults, cfg.Hooks.QwenMaxTokens)
	fmt.Println(block)
	return nil
}

// runHookRunCursor handles the Cursor prompt hook: embeds the query,
// searches Qdrant, formats a context block, and prints it.
func runHookRunCursor(project, query string, topK int) error {
	if query == "" {
		return nil // nothing to do
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
		time.Duration(cfg.LLM.RequestTimeout)*time.Second, time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

	ctx := context.Background()

	vecs, err := llmClient.EmbedQuery(ctx, cfg.Embedding.Model, cfg.Embedding.QueryInstruction, []string{query})
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) == 0 {
		return fmt.Errorf("embedder returned no vectors")
	}

	collection := "nav_" + project
	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	results, err := qdrantClient.Search(ctx, collection, vecs[0], overFetch(topK), cfg.Hooks.CursorMinScore, nil)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}
	results = topN(collapseChunks(results), topK)

	// Convert to hook.ContextResult.
	ctxResults := make([]hook.ContextResult, 0, len(results))
	for _, r := range results {
		ctxResults = append(ctxResults, hook.ContextResult{
			Score:   r.Score,
			Symbol:  r.Payload.Symbol,
			Type:    r.Payload.Type,
			File:    r.Payload.FilePath,
			Purpose: r.Payload.Summary,
			Code:    r.Payload.Content,
		})
	}

	block := hook.FormatContextBlock(project, query, ctxResults, cfg.Hooks.CursorMaxTokens)
	fmt.Println(block)
	return nil
}

// runHookRunOpenCode handles the OpenCode prompt hook: embeds the query,
// searches Qdrant, formats a context block, and prints it.
func runHookRunOpenCode(project, query string, topK int) error {
	if query == "" {
		return nil // nothing to do
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
		time.Duration(cfg.LLM.RequestTimeout)*time.Second, time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

	ctx := context.Background()

	vecs, err := llmClient.EmbedQuery(ctx, cfg.Embedding.Model, cfg.Embedding.QueryInstruction, []string{query})
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) == 0 {
		return fmt.Errorf("embedder returned no vectors")
	}

	collection := "nav_" + project
	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	results, err := qdrantClient.Search(ctx, collection, vecs[0], overFetch(topK), cfg.Hooks.OpenCodeMinScore, nil)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}
	results = topN(collapseChunks(results), topK)

	// Convert to hook.ContextResult.
	ctxResults := make([]hook.ContextResult, 0, len(results))
	for _, r := range results {
		ctxResults = append(ctxResults, hook.ContextResult{
			Score:   r.Score,
			Symbol:  r.Payload.Symbol,
			Type:    r.Payload.Type,
			File:    r.Payload.FilePath,
			Purpose: r.Payload.Summary,
			Code:    r.Payload.Content,
		})
	}

	block := hook.FormatContextBlock(project, query, ctxResults, cfg.Hooks.OpenCodeMaxTokens)
	fmt.Println(block)
	return nil
}
