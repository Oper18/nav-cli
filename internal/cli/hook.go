package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"nav/config"
	"nav/internal/hook"
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
		installed, err := hook.Install(path, cfg)
		if err != nil {
			return fmt.Errorf("installing git hook: %w", err)
		}
		if installed {
			fmt.Printf("nav git hooks installed in %s/.git/hooks/ (pre-commit + post-merge + post-rewrite + reference-transaction)\n", path)
		} else {
			fmt.Printf("nav git hooks already installed in %s/.git/hooks/, skipping\n", path)
		}

	case "claude":
		project, _, err := services.ResolveProject(args, hookInstallPath)
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
		installed, err := hook.InstallClaude(settingsPath, project, topK)
		if err != nil {
			return fmt.Errorf("installing Claude hook: %w", err)
		}
		if installed {
			fmt.Printf("nav Claude hook installed in %s\n", settingsPath)
		} else {
			fmt.Printf("nav Claude hook already installed in %s, skipping\n", settingsPath)
		}

	case "qwen":
		project, _, err := services.ResolveProject(args, hookInstallPath)
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
		installed, err := hook.InstallQwen(settingsPath, project, topK)
		if err != nil {
			return fmt.Errorf("installing Qwen hook: %w", err)
		}
		if installed {
			fmt.Printf("nav Qwen hook installed in %s\n", settingsPath)
		} else {
			fmt.Printf("nav Qwen hook already installed in %s, skipping\n", settingsPath)
		}

	case "cursor":
		project, _, err := services.ResolveProject(args, hookInstallPath)
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
		installed, err := hook.InstallCursor(settingsPath, project, topK)
		if err != nil {
			return fmt.Errorf("installing Cursor hook: %w", err)
		}
		if installed {
			fmt.Printf("nav Cursor hook installed in %s\n", settingsPath)
		} else {
			fmt.Printf("nav Cursor hook already installed in %s, skipping\n", settingsPath)
		}

	case "opencode":
		project, _, err := services.ResolveProject(args, hookInstallPath)
		if err != nil {
			return err
		}
		dir := hookInstallPath
		if dir == "" {
			dir = "."
		}
		topK := cfg.Hooks.OpenCodeTopK
		installed, err := hook.InstallOpenCode(dir, project, topK)
		if err != nil {
			return fmt.Errorf("installing OpenCode hook: %w", err)
		}
		if installed {
			fmt.Printf("nav OpenCode hook installed in %s/.opencode/plugins/nav-hook.js\n", dir)
		} else {
			fmt.Printf("nav OpenCode hook already installed in %s/.opencode/plugins/nav-hook.js, skipping\n", dir)
		}

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
		fmt.Printf("nav git hooks removed from %s/.git/hooks/ (pre-commit + post-merge + post-rewrite + reference-transaction)\n", path)

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
	hookRunCmd.Flags().StringVar(&hookRunType, "type", "", `Hook type: "git", "git-post-merge", "claude", "claude-session-start", "qwen", "cursor", or "opencode" (required)`)
	hookRunCmd.Flags().StringVar(&hookRunPath, "path", ".", "Repository path (for git hooks)")
	hookRunCmd.Flags().IntVar(&hookRunTop, "top", 5, "Number of results to inject (for claude, qwen, cursor, and opencode hooks)")
	hookRunCmd.Flags().StringVar(&hookRunQuery, "query", "", "Query text (for claude, qwen, cursor, and opencode hooks)")

	_ = hookRunCmd.MarkFlagRequired("type")
}

func runHookRun(cmd *cobra.Command, args []string) error {
	switch hookRunType {
	case "git":
		return runHookRunGit(hookRunPath)

	case "git-post-merge":
		return runHookRunPostMerge(hookRunPath)

	case "claude":
		project, path, err := services.ResolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunClaude(project, path, hookRunQuery, hookRunTop)

	case "claude-session-start":
		project, path, err := services.ResolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunClaudeSessionStart(project, path)

	case "qwen":
		project, path, err := services.ResolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunQwen(project, path, hookRunQuery, hookRunTop)

	case "cursor":
		project, path, err := services.ResolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunCursor(project, path, hookRunQuery, hookRunTop)

	case "opencode":
		project, path, err := services.ResolveProject(args, hookRunPath)
		if err != nil {
			return err
		}
		return runHookRunOpenCode(project, path, hookRunQuery, hookRunTop)

	default:
		return fmt.Errorf("unknown hook type %q; must be \"git\", \"git-post-merge\", \"claude\", \"claude-session-start\", \"qwen\", \"cursor\", or \"opencode\"", hookRunType)
	}
}

// runHookRunClaudeSessionStart handles Claude Code's SessionStart hook: it
// prints the knowledge graph's cached digest (nav graph summary) plus the
// full project structure (every package and the files it contains), so a
// new session starts already knowing the codebase's layout — straight from
// the graph nav sync built — instead of having to explore it with
// find/ls/tree first. It does not sync first — the digest reflects whatever
// the last sync left in the current branch's .nav/nav-<branch>.db, and
// syncing here would add hook-startup latency for a summary that's meant to
// be a cheap, already-cached read.
func runHookRunClaudeSessionStart(project, path string) error {
	digest, err := services.SessionStartDigest(path)
	if err != nil {
		return fmt.Errorf("building session start context: %w", err)
	}
	fmt.Println(digest)
	return nil
}

// runHookRunGit handles the git pre-commit hook and runHookRunPostMerge the
// post-merge/post-rewrite/reference-transaction hooks (every flavor of
// `git pull`, see git.go): both just trigger a lazy sync — on commit and on
// pull, the only two events that can actually change what's on disk.
// Pushing doesn't, so there is deliberately no pre-push hook triggering a
// sync. Routing through services.GitHookSync — rather than diffing staged/
// merged files and re-indexing them directly — means every git hook run is
// validated against the same SQLite manifest the prompt hooks use, so a file
// already synced (by a previous hook run, or by a prompt hook in between) is
// never re-embedded for nothing. On the pull side, the lazy sync re-diffs
// against the last synced HEAD, so every object touched by the incoming
// commits gets re-parsed and, where its content hash actually changed,
// re-embedded and written back to Qdrant and the local SQLite state.
func runHookRunGit(repoPath string) error {
	return runGitHookSync(repoPath)
}

func runHookRunPostMerge(repoPath string) error {
	return runGitHookSync(repoPath)
}

func runGitHookSync(repoPath string) error {
	result, err := services.GitHookSync(repoPath)
	if err != nil {
		return fmt.Errorf("syncing: %w", err)
	}
	if result.Skipped {
		fmt.Println("nav: sync skipped, another sync already running")
		return nil
	}
	fmt.Printf("nav: %s\n", result.Summary())
	return nil
}

// runHookRunAssistant is the shared core of every AI-assistant prompt hook:
// it searches the project (syncing first) via services.HookSearch and prints
// the formatted <nav-context> block. An empty query is a no-op, matching the
// prompt hooks' "nothing to inject" case.
func runHookRunAssistant(project, path, query string, topK int, minScore float64, maxTokens int) error {
	if query == "" {
		return nil
	}
	results, err := services.HookSearch(context.Background(), project, path, query, topK, minScore)
	if err != nil {
		return err
	}
	fmt.Println(hook.FormatContextBlock(project, query, results, maxTokens))
	return nil
}

func runHookRunClaude(project, path, query string, topK int) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	return runHookRunAssistant(project, path, query, topK, cfg.Hooks.ClaudeMinScore, cfg.Hooks.ClaudeMaxTokens)
}

func runHookRunQwen(project, path, query string, topK int) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	return runHookRunAssistant(project, path, query, topK, cfg.Hooks.QwenMinScore, cfg.Hooks.QwenMaxTokens)
}

func runHookRunCursor(project, path, query string, topK int) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	return runHookRunAssistant(project, path, query, topK, cfg.Hooks.CursorMinScore, cfg.Hooks.CursorMaxTokens)
}

func runHookRunOpenCode(project, path, query string, topK int) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	return runHookRunAssistant(project, path, query, topK, cfg.Hooks.OpenCodeMinScore, cfg.Hooks.OpenCodeMaxTokens)
}
