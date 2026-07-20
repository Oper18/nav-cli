package hook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nav/config"
)

const gitHookScript = `#!/usr/bin/env bash
# nav pre-commit hook
[ -n "$%s" ] && exit 0
nav hook run --type git --path "$(git rev-parse --show-toplevel)"
exit 0
`

const gitPostMergeHookScript = `#!/usr/bin/env bash
# nav post-merge hook
nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
exit 0
`

// Install writes the nav pre-commit and post-merge hooks to <repoPath>/.git/hooks/.
// It sets the skip env var name from cfg.Hooks.GitSkipEnv.
// If a hook already exists and is NOT a nav hook, it appends the nav call
// rather than overwriting.
func Install(repoPath string, cfg *config.Config) error {
	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return fmt.Errorf("not a git repository (no .git found in %s): %w", repoPath, err)
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}

	if err := installHook(hooksDir, "pre-commit", "# nav-hook\n"+fmt.Sprintf(gitHookScript, cfg.Hooks.GitSkipEnv), "# nav-hook-append\nnav hook run --type git --path \"$(git rev-parse --show-toplevel)\"\n"); err != nil {
		return fmt.Errorf("installing pre-commit hook: %w", err)
	}

	if err := installHook(hooksDir, "post-merge", "# nav-hook\n"+gitPostMergeHookScript, "# nav-hook-append\nnav hook run --type git-post-merge --path \"$(git rev-parse --show-toplevel)\"\n"); err != nil {
		return fmt.Errorf("installing post-merge hook: %w", err)
	}

	return nil
}

// installHook writes or appends a nav hook script to the given hook file.
func installHook(hooksDir, hookName, fullScript, appendBlock string) error {
	hookPath := filepath.Join(hooksDir, hookName)

	existing, err := os.ReadFile(hookPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading existing hook: %w", err)
	}

	if os.IsNotExist(err) || len(existing) == 0 {
		if err := os.WriteFile(hookPath, []byte(fullScript), 0755); err != nil {
			return fmt.Errorf("writing %s hook: %w", hookName, err)
		}
		return nil
	}

	content := string(existing)

	if strings.Contains(content, "# nav-hook") {
		return nil
	}

	appended := content
	if !strings.HasSuffix(appended, "\n") {
		appended += "\n"
	}
	appended += "\n" + appendBlock

	if err := os.WriteFile(hookPath, []byte(appended), 0755); err != nil {
		return fmt.Errorf("appending nav %s hook: %w", hookName, err)
	}
	return nil
}

// Uninstall removes the nav pre-commit and post-merge hooks from <repoPath>/.git/hooks/.
// If a hook file was entirely nav-generated (contains the nav marker), it removes the file.
// If it was appended, it removes only the nav lines.
func Uninstall(repoPath string) error {
	hooksDir := filepath.Join(repoPath, ".git", "hooks")

	if err := uninstallHook(hooksDir, "pre-commit"); err != nil {
		return fmt.Errorf("uninstalling pre-commit hook: %w", err)
	}

	if err := uninstallHook(hooksDir, "post-merge"); err != nil {
		return fmt.Errorf("uninstalling post-merge hook: %w", err)
	}

	return nil
}

// uninstallHook removes the nav portion of a git hook file.
func uninstallHook(hooksDir, hookName string) error {
	hookPath := filepath.Join(hooksDir, hookName)

	data, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s hook: %w", hookName, err)
	}

	content := string(data)

	if strings.Contains(content, "# nav-hook\n") {
		if err := os.Remove(hookPath); err != nil {
			return fmt.Errorf("removing %s hook: %w", hookName, err)
		}
		return nil
	}

	if idx := strings.Index(content, "\n# nav-hook-append\n"); idx >= 0 {
		trimmed := strings.TrimRight(content[:idx], "\n") + "\n"
		if err := os.WriteFile(hookPath, []byte(trimmed), 0755); err != nil {
			return fmt.Errorf("writing trimmed %s hook: %w", hookName, err)
		}
	}

	return nil
}

// Run is called by the pre-commit hook itself.
// It reads staged file paths from `git diff --cached --name-only`,
// detects which are source files, and returns them grouped by operation:
// changed files (to re-index) and deleted files (to remove from Qdrant).
func Run(repoPath string) (changed []string, deleted []string, err error) {
	return StagedFiles(repoPath)
}

// StagedFiles returns files staged for commit (added + modified + deleted).
func StagedFiles(repoPath string) (changed []string, deleted []string, err error) {
	changedOut, err := runGit(repoPath, "diff", "--cached", "--name-only", "--diff-filter=ACMRT")
	if err != nil {
		return nil, nil, fmt.Errorf("git diff (changed): %w", err)
	}

	deletedOut, err := runGit(repoPath, "diff", "--cached", "--name-only", "--diff-filter=D")
	if err != nil {
		return nil, nil, fmt.Errorf("git diff (deleted): %w", err)
	}

	changed = parseLines(changedOut)
	deleted = parseLines(deletedOut)
	return changed, deleted, nil
}

// MergedFiles returns files that changed during a merge (post-merge hook).
// It uses ORIG_HEAD (set by git before the merge) to diff against HEAD.
func MergedFiles(repoPath string) (changed []string, deleted []string, err error) {
	changedOut, err := runGit(repoPath, "diff", "--name-only", "ORIG_HEAD", "HEAD", "--diff-filter=ACMRT")
	if err != nil {
		return nil, nil, fmt.Errorf("git diff (merged changed): %w", err)
	}

	deletedOut, err := runGit(repoPath, "diff", "--name-only", "ORIG_HEAD", "HEAD", "--diff-filter=D")
	if err != nil {
		return nil, nil, fmt.Errorf("git diff (merged deleted): %w", err)
	}

	changed = parseLines(changedOut)
	deleted = parseLines(deletedOut)
	return changed, deleted, nil
}

// runGit executes a git command inside repoPath and returns its stdout as a string.
func runGit(repoPath string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseLines splits newline-delimited output, trims whitespace, and drops empty entries.
func parseLines(raw string) []string {
	parts := strings.Split(raw, "\n")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
