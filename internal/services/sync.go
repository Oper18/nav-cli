package services

import (
	"context"
	"fmt"
)

// SyncSinceResult reports what a commit-log replay sync (`nav sync --since`)
// found. CommitCount is 0 when no commits matched since; ChangedFiles is
// empty when commits matched but touched nothing indexable.
type SyncSinceResult struct {
	CommitCount  int
	ChangedFiles []string
}

// SyncSince walks `git log` for commits after since (a date, hash, or ref)
// and re-indexes every file they touched, ignoring the lazy-sync manifest.
// It is the commit-log replay mode used to catch up commits made with
// NAV_SKIP set. When dryRun is true, changed files are reported but not
// re-indexed.
func SyncSince(ctx context.Context, project, repoPath, since string, dryRun bool) (SyncSinceResult, error) {
	hashes, err := syncCommitHashes(repoPath, since)
	if err != nil {
		return SyncSinceResult{}, fmt.Errorf("listing commits: %w", err)
	}
	if len(hashes) == 0 {
		return SyncSinceResult{}, nil
	}

	seen := make(map[string]bool)
	var changedFiles []string
	for _, hash := range hashes {
		files, err := changedFilesInCommit(repoPath, hash)
		if err != nil {
			// Non-fatal: skip this commit.
			fmt.Printf("warn: diff-tree %s: %v\n", hash, err)
			continue
		}
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				changedFiles = append(changedFiles, f)
			}
		}
	}

	result := SyncSinceResult{CommitCount: len(hashes), ChangedFiles: changedFiles}
	if len(changedFiles) == 0 || dryRun {
		return result, nil
	}

	if err := IndexSpecificFiles(ctx, project, repoPath, "", "", 4, false, changedFiles, []string{}, false); err != nil {
		return result, err
	}
	return result, nil
}

// syncCommitHashes returns the commit hashes touched since the given date
// (or ref): git log --format=%H --since=<since> -- . . Only called from the
// --since commit-replay path — since is always non-empty here.
func syncCommitHashes(repoPath, since string) ([]string, error) {
	out, err := RunGitCmd(repoPath, "log", "--format=%H", "--since="+since, "--", ".")
	if err != nil {
		return nil, err
	}
	return SplitLines(out), nil
}

// changedFilesInCommit returns the list of files changed in the given commit.
func changedFilesInCommit(repoPath, hash string) ([]string, error) {
	out, err := RunGitCmd(repoPath, "diff-tree", "--no-commit-id", "-r", "--name-only", "--diff-filter=ACMRT", hash)
	if err != nil {
		return nil, err
	}
	return SplitLines(out), nil
}
