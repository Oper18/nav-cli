package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var (
	syncPath   string
	syncSince  string
	syncDryRun bool
)

var syncCmd = &cobra.Command{
	Use:   "sync [project]",
	Short: "Re-index files changed since a given date or in the last 50 commits",
	Long: "Re-index files changed since a given date or in the last 50 commits.\n\n" +
		"Both the project name and --path are optional. When the project name is\n" +
		"omitted it defaults to the basename of the current directory; when --path is\n" +
		"omitted the path defaults to the project's registered path, or the current\n" +
		"directory.",
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
	syncCmd.Flags().StringVar(&syncPath, "path", "", "Path to the repository root (default: project path or current directory)")
	syncCmd.Flags().StringVar(&syncSince, "since", "", "Only consider commits after this date (e.g. 2024-01-01)")
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Print files that would be re-indexed without doing so")
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	project, path, err := resolveProject(args, syncPath)
	if err != nil {
		return err
	}

	// 1. Get commit hashes.
	hashes, err := syncCommitHashes(path, syncSince)
	if err != nil {
		return fmt.Errorf("listing commits: %w", err)
	}
	if len(hashes) == 0 {
		fmt.Println("No commits found.")
		return nil
	}

	// 2. Collect unique changed files from all commits.
	seen := make(map[string]bool)
	var changedFiles []string

	for _, hash := range hashes {
		files, err := changedFilesInCommit(path, hash)
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

	if len(changedFiles) == 0 {
		fmt.Println("No changed files detected.")
		return nil
	}

	// 3. Dry-run: list the files and return.
	if syncDryRun {
		fmt.Printf("Files that would be re-indexed (%d):\n", len(changedFiles))
		for _, f := range changedFiles {
			fmt.Printf("  %s\n", f)
		}
		return nil
	}

	// 4. Re-index changed files using shared indexing logic.
	if err := indexSpecificFiles(ctx, project, path, "", "", 4, false, changedFiles, []string{}); err != nil {
		return err
	}

	return nil
}

// syncCommitHashes returns the commit hashes to consider.
// When since is non-empty it runs: git log --format=%H --since=<since> -- .
// Otherwise it runs: git log --format=%H -50
func syncCommitHashes(repoPath, since string) ([]string, error) {
	var gitArgs []string
	if since != "" {
		gitArgs = []string{"log", "--format=%H", "--since=" + since, "--", "."}
	} else {
		gitArgs = []string{"log", "--format=%H", "-50"}
	}

	out, err := runGitCmd(repoPath, gitArgs...)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// changedFilesInCommit returns the list of files changed in the given commit.
func changedFilesInCommit(repoPath, hash string) ([]string, error) {
	out, err := runGitCmd(repoPath, "diff-tree", "--no-commit-id", "-r", "--name-only", "--diff-filter=ACMRT", hash)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// runGitCmd executes a git command inside repoPath and returns stdout.
func runGitCmd(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// splitLines splits newline-delimited output and drops empty lines.
func splitLines(raw string) []string {
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
