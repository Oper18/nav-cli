package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"nav/config"
	"nav/internal/db"
	"nav/internal/hook"
)

// DeleteProject permanently removes every trace of project nav knows about:
// its Qdrant collection, all local SQLite index state (chunk manifest +
// knowledge graph, every branch, under ~/.nav/projects/<project>), its
// generated README (~/.nav-cli/projects/<project>/readme.md), its entry in
// ~/.nav-cli/projects.yaml, and — when repoPath is known — any legacy
// in-repo .nav/ directory left over from before nav moved that state out of
// the working tree (a project fully migrated by ensureMigratedFromRepo won't
// have one left; this only matters for a project that was never touched
// since, or was deleted before ever syncing again), plus every local
// AI-assistant/git hook nav may have installed in that directory (see
// uninstallProjectHooks). repoPath may be "" when the project's registered
// path is unknown; both of those repoPath-scoped steps are skipped in that
// case, everything else still runs.
//
// Unlike ResetProject (which wipes local+Qdrant state so a project can be
// rebuilt in place), DeleteProject also drops the registry entry and every
// project-scoped file — there is nothing left to "reindex into" afterwards.
func DeleteProject(ctx context.Context, project, repoPath, collection string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}
	if err := EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	if collection == "" {
		collection = "nav_" + project
	}
	if err := qdrantClient.DeleteCollection(ctx, collection); err != nil {
		return fmt.Errorf("deleting qdrant collection %q: %w", collection, err)
	}

	// Local SQLite state: the whole project directory, not just the
	// per-branch db files ResetAll targets — this is a full delete, not a
	// wipe-and-reindex-in-place.
	if navDir, err := db.Dir(project); err == nil {
		if err := os.RemoveAll(navDir); err != nil {
			return fmt.Errorf("removing local index state %s: %w", navDir, err)
		}
	}

	if repoPath != "" {
		// A legacy in-repo .nav/ this project never got the chance to
		// migrate out of (or that survived a migration failure).
		// Best-effort: a missing directory or removal error here doesn't
		// block the rest of the delete.
		legacyDir := filepath.Join(repoPath, ".nav")
		if _, statErr := os.Stat(legacyDir); statErr == nil {
			if err := os.RemoveAll(legacyDir); err != nil {
				fmt.Fprintf(os.Stderr, "nav: warn: removing legacy state %s: %v\n", legacyDir, err)
			}
		}

		uninstallProjectHooks(repoPath)
	}

	if err := os.RemoveAll(config.ProjectDir(project)); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: removing %s: %v\n", config.ProjectDir(project), err)
	}

	if err := config.RemoveProject(project); err != nil {
		return fmt.Errorf("removing project registration: %w", err)
	}

	return nil
}

// uninstallProjectHooks removes every local (project-scoped, not --global)
// hook nav may have installed in repoPath — git pre-commit/post-merge/
// post-rewrite/reference-transaction, plus the Claude/Qwen/Cursor/OpenCode
// prompt hooks — as part of deleting a project whose repo is still around.
// Each Uninstall* call already handles "not installed" as a no-op rather
// than an error, so this runs unconditionally; a hook that was never
// installed there in the first place simply does nothing. Failures are
// warnings, not fatal — a hook file nav can't remove (e.g. permissions)
// shouldn't abort the rest of the delete.
//
// The Qwen settings path is built directly (repoPath/.qwen/settings.json)
// rather than via hook.QwenDefaultSettingsPath, which — unlike its Claude/
// Cursor counterparts — falls back to the *global* ~/.qwen/settings.json
// when the repo has no local one. Using that helper here would mean
// deleting a project that never had a local Qwen hook could silently strip
// the user's global Qwen hook instead; this is a delete scoped to repoPath,
// so it must never reach outside it.
func uninstallProjectHooks(repoPath string) {
	if err := hook.Uninstall(repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: removing git hooks from %s: %v\n", repoPath, err)
	}
	if err := hook.UninstallClaude(hook.DefaultSettingsPath(repoPath)); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: removing Claude hook from %s: %v\n", repoPath, err)
	}
	if err := hook.UninstallQwen(filepath.Join(repoPath, ".qwen", "settings.json")); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: removing Qwen hook from %s: %v\n", repoPath, err)
	}
	if err := hook.UninstallCursor(hook.CursorDefaultSettingsPath(repoPath)); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: removing Cursor hook from %s: %v\n", repoPath, err)
	}
	if err := hook.UninstallOpenCode(repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: removing OpenCode hook from %s: %v\n", repoPath, err)
	}
}
