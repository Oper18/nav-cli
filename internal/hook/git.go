package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nav/config"
)

// The shebang must stay on line 1 for the OS to recognize this as a bash
// script when git execve()s it directly; the "# nav-hook" marker therefore
// goes on line 2, never prepended ahead of the shebang.
const gitHookScript = `#!/usr/bin/env bash
# nav-hook
# nav pre-commit hook
[ -n "$%s" ] && exit 0
nav hook run --type git --path "$(git rev-parse --show-toplevel)"
exit 0
`

const gitPostMergeHookScript = `#!/usr/bin/env bash
# nav-hook
# nav post-merge hook
nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
exit 0
`

// gitPostRewriteHookScript backs the post-rewrite hook, which git invokes
// after commands that rewrite commits (rebase, commit --amend) with $1 set
// to "rebase" or "amend". "git pull --rebase" fires this with $1=rebase — a
// plain "git pull" instead fires post-merge (even on a fast-forward), so
// between the two, every flavor of "git pull" ends up triggering a sync.
// Amends are skipped: they don't necessarily touch the working tree beyond
// what the commit already captured, and re-syncing on every --amend would
// just be noise.
const gitPostRewriteHookScript = `#!/usr/bin/env bash
# nav-hook
# nav post-rewrite hook
[ "$1" = "rebase" ] || exit 0
nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
exit 0
`

// gitReferenceTransactionHookScript backs the reference-transaction hook —
// the one that actually makes "every flavor of git pull" true. git skips
// its merge/rebase machinery entirely for a pure fast-forward (the most
// common pull of all), so neither post-merge nor post-rewrite fires for it;
// reference-transaction, by contrast, fires after ANY ref update,
// fast-forward included. It's filtered to only the checked-out branch's own
// ref (or HEAD) so it stays quiet on a plain `git fetch`, which only moves
// remote-tracking refs, not the local branch.
const gitReferenceTransactionHookScript = `#!/usr/bin/env bash
# nav-hook
# nav reference-transaction hook
[ "$1" = "committed" ] || exit 0
branch="refs/heads/$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
while read -r old new ref; do
  if [ "$ref" = "$branch" ] || [ "$ref" = "HEAD" ]; then
    nav hook run --type git-post-merge --path "$(git rev-parse --show-toplevel)"
    exit 0
  fi
done
exit 0
`

// Install writes the nav pre-commit, post-merge, post-rewrite, and
// reference-transaction hooks to <repoPath>/.git/hooks/. It sets the skip
// env var name from cfg.Hooks.GitSkipEnv. If a hook already exists and is
// NOT a nav hook, it appends the nav call rather than overwriting. It
// returns installed=false when every hook was already present, so callers
// can tell a no-op apart from a fresh install.
//
// There is deliberately no pre-push hook: indexing/embedding only ever runs
// on commit (pre-commit) and on pull (post-merge/post-rewrite/
// reference-transaction, covering every flavor of `git pull`) — a push
// doesn't change what's on disk, so there's nothing for it to (re-)index.
func Install(repoPath string, cfg *config.Config) (installed bool, err error) {
	gitDir, err := resolveGitDir(repoPath)
	if err != nil {
		return false, err
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return false, fmt.Errorf("creating hooks directory: %w", err)
	}

	preCommitInstalled, err := installHook(hooksDir, "pre-commit", fmt.Sprintf(gitHookScript, cfg.Hooks.GitSkipEnv), "# nav-hook-append\nnav hook run --type git --path \"$(git rev-parse --show-toplevel)\"\n")
	if err != nil {
		return false, fmt.Errorf("installing pre-commit hook: %w", err)
	}

	postMergeInstalled, err := installHook(hooksDir, "post-merge", gitPostMergeHookScript, "# nav-hook-append\nnav hook run --type git-post-merge --path \"$(git rev-parse --show-toplevel)\"\n")
	if err != nil {
		return false, fmt.Errorf("installing post-merge hook: %w", err)
	}

	postRewriteInstalled, err := installHook(hooksDir, "post-rewrite", gitPostRewriteHookScript, "# nav-hook-append\n[ \"$1\" = \"rebase\" ] && nav hook run --type git-post-merge --path \"$(git rev-parse --show-toplevel)\"\n")
	if err != nil {
		return false, fmt.Errorf("installing post-rewrite hook: %w", err)
	}

	refTxAppend := "# nav-hook-append\n" +
		"if [ \"$1\" = \"committed\" ]; then\n" +
		"  branch=\"refs/heads/$(git rev-parse --abbrev-ref HEAD 2>/dev/null)\"\n" +
		"  while read -r old new ref; do\n" +
		"    if [ \"$ref\" = \"$branch\" ] || [ \"$ref\" = \"HEAD\" ]; then\n" +
		"      nav hook run --type git-post-merge --path \"$(git rev-parse --show-toplevel)\"\n" +
		"      break\n" +
		"    fi\n" +
		"  done\n" +
		"fi\n"
	refTxInstalled, err := installHook(hooksDir, "reference-transaction", gitReferenceTransactionHookScript, refTxAppend)
	if err != nil {
		return false, fmt.Errorf("installing reference-transaction hook: %w", err)
	}

	return preCommitInstalled || postMergeInstalled || postRewriteInstalled || refTxInstalled, nil
}

// HooksDir returns the directory repoPath's git hooks live in (or would be
// installed to) — ordinarily <repoPath>/.git/hooks, but
// <superproject>/.git/modules/<name>/hooks for a submodule checkout. It's
// exposed for callers (e.g. the `nav hook install/uninstall` CLI) that want
// to report the real on-disk location rather than assuming <repoPath>/.git
// is always a directory.
func HooksDir(repoPath string) (string, error) {
	gitDir, err := resolveGitDir(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, "hooks"), nil
}

// resolveGitDir returns the actual git directory for repoPath — the
// directory that holds hooks/, refs/, and objects/. For an ordinary
// repository (including the top-level checkout of a repo that has
// submodules) this is simply <repoPath>/.git. For a git submodule, however,
// <repoPath>/.git is not a directory at all: git replaces it with a *file*
// containing a single "gitdir: <path>" line pointing at the real git
// directory, normally <superproject>/.git/modules/<name>/ — that's where
// the submodule's own hooks/ lives, and hooks installed there fire only for
// commits/pulls made inside the submodule, leaving the superproject (and
// any of its other submodules) untouched. Naively treating
// <repoPath>/.git/hooks as the hooks directory would try to MkdirAll a
// "hooks" child of a regular file and fail outright.
// See https://git-scm.com/docs/gitrepository-layout#_the_gitdir_file.
func resolveGitDir(repoPath string) (string, error) {
	gitPath := filepath.Join(repoPath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", fmt.Errorf("not a git repository (no .git found in %s): %w", repoPath, err)
	}
	if info.IsDir() {
		return gitPath, nil
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", gitPath, err)
	}
	line := strings.TrimSpace(string(data))
	target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if target == "" || target == line {
		return "", fmt.Errorf("%s is not a directory and doesn't look like a submodule gitdir pointer (expected a \"gitdir: <path>\" line)", gitPath)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(repoPath, target)
	}
	target = filepath.Clean(target)
	fi, statErr := os.Stat(target)
	if statErr != nil {
		return "", fmt.Errorf("resolving submodule git dir %s (pointed to by %s): %w", target, gitPath, statErr)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("submodule git dir %s (pointed to by %s) is not a directory", target, gitPath)
	}
	return target, nil
}

// installHook writes or appends a nav hook script to the given hook file. It
// returns installed=false when the file already carried a nav-hook marker,
// so no write happened.
func installHook(hooksDir, hookName, fullScript, appendBlock string) (installed bool, err error) {
	hookPath := filepath.Join(hooksDir, hookName)

	existing, err := os.ReadFile(hookPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading existing hook: %w", err)
	}

	if os.IsNotExist(err) || len(existing) == 0 {
		if err := os.WriteFile(hookPath, []byte(fullScript), 0755); err != nil {
			return false, fmt.Errorf("writing %s hook: %w", hookName, err)
		}
		return true, nil
	}

	content := string(existing)

	if strings.Contains(content, "# nav-hook") {
		return false, nil // already installed
	}

	appended := content
	if !strings.HasSuffix(appended, "\n") {
		appended += "\n"
	}
	appended += "\n" + appendBlock

	if err := os.WriteFile(hookPath, []byte(appended), 0755); err != nil {
		return false, fmt.Errorf("appending nav %s hook: %w", hookName, err)
	}
	return true, nil
}

// Uninstall removes the nav pre-commit, post-merge, post-rewrite, and
// reference-transaction hooks from repoPath's git hooks directory (ordinarily
// <repoPath>/.git/hooks, or <superproject>/.git/modules/<name>/hooks for a
// submodule — see resolveGitDir). If a hook file was entirely nav-generated
// (contains the nav marker), it removes the file. If it was appended, it
// removes only the nav lines. It also cleans up a pre-push hook left behind
// by an older nav version, which used to sync on push; nav no longer
// installs one.
//
// repoPath not resolving to a git directory at all (already deleted from
// disk, never was a repo, ...) is treated as a no-op rather than an error —
// callers like `nav delete` invoke this unconditionally as cleanup, and
// there's nothing to uninstall from a repo that isn't there.
func Uninstall(repoPath string) error {
	gitDir, err := resolveGitDir(repoPath)
	if err != nil {
		return nil
	}
	hooksDir := filepath.Join(gitDir, "hooks")

	if err := uninstallHook(hooksDir, "pre-commit"); err != nil {
		return fmt.Errorf("uninstalling pre-commit hook: %w", err)
	}

	if err := uninstallHook(hooksDir, "pre-push"); err != nil {
		return fmt.Errorf("uninstalling pre-push hook: %w", err)
	}

	if err := uninstallHook(hooksDir, "post-merge"); err != nil {
		return fmt.Errorf("uninstalling post-merge hook: %w", err)
	}

	if err := uninstallHook(hooksDir, "post-rewrite"); err != nil {
		return fmt.Errorf("uninstalling post-rewrite hook: %w", err)
	}

	if err := uninstallHook(hooksDir, "reference-transaction"); err != nil {
		return fmt.Errorf("uninstalling reference-transaction hook: %w", err)
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
