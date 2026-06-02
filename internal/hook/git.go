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

// Install writes the nav pre-commit hook to <repoPath>/.git/hooks/pre-commit.
// It sets the skip env var name from cfg.Hooks.GitSkipEnv.
// If a pre-commit hook already exists and is NOT a nav hook, it appends the nav call
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

	hookPath := filepath.Join(hooksDir, "pre-commit")

	existing, err := os.ReadFile(hookPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading existing hook: %w", err)
	}

	if os.IsNotExist(err) || len(existing) == 0 {
		// No existing hook — write the full script with our marker on the first line.
		script := "# nav-hook\n" + fmt.Sprintf(gitHookScript, cfg.Hooks.GitSkipEnv)
		if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
			return fmt.Errorf("writing pre-commit hook: %w", err)
		}
		return nil
	}

	content := string(existing)

	// Already a nav hook — nothing to do.
	if strings.Contains(content, "# nav-hook") {
		return nil
	}

	// Foreign hook — append our block.
	appended := content
	if !strings.HasSuffix(appended, "\n") {
		appended += "\n"
	}
	appended += "\n# nav-hook-append\nnav hook run --type git --path \"$(git rev-parse --show-toplevel)\"\n"

	if err := os.WriteFile(hookPath, []byte(appended), 0755); err != nil {
		return fmt.Errorf("appending nav hook: %w", err)
	}
	return nil
}

// Uninstall removes the nav pre-commit hook from <repoPath>/.git/hooks/pre-commit.
// If the file was entirely nav-generated (contains the nav marker), it removes the file.
// If it was appended, it removes only the nav lines.
func Uninstall(repoPath string) error {
	hookPath := filepath.Join(repoPath, ".git", "hooks", "pre-commit")

	data, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading pre-commit hook: %w", err)
	}

	content := string(data)

	// Entirely nav-owned — delete the file.
	if strings.Contains(content, "# nav-hook\n") {
		if err := os.Remove(hookPath); err != nil {
			return fmt.Errorf("removing pre-commit hook: %w", err)
		}
		return nil
	}

	// Appended block — strip from the marker to end of nav block.
	if idx := strings.Index(content, "\n# nav-hook-append\n"); idx >= 0 {
		trimmed := strings.TrimRight(content[:idx], "\n") + "\n"
		if err := os.WriteFile(hookPath, []byte(trimmed), 0755); err != nil {
			return fmt.Errorf("writing trimmed hook: %w", err)
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
