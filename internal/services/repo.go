package services

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"nav/config"
	"nav/internal/db"
)

// FetchAll runs `git fetch --all --prune` against repoPath, streaming its
// output directly to stdout/stderr.
func FetchAll(repoPath string) error {
	c := exec.Command("git", "-C", repoPath, "fetch", "--all", "--prune")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("git fetch --all --prune: %w", err)
	}
	return nil
}

// CleanGoneBranches finds local branches whose upstream is gone, purges
// their points from collection (or "nav_<project>" when collection is
// empty), removes their per-branch local SQLite state (chunk manifest +
// knowledge graph under ~/.nav/projects/<project>), and deletes the
// branches. It returns the branch names that were cleaned; a nil slice with
// a nil error means there was nothing to do.
func CleanGoneBranches(ctx context.Context, project, repoPath, collection string) ([]string, error) {
	gone, err := goneBranches(repoPath)
	if err != nil {
		return nil, err
	}
	if len(gone) == 0 {
		return nil, nil
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}

	if collection == "" {
		collection = "nav_" + project
	}

	if err := EnsureLocalQdrant(cfg); err != nil {
		return nil, fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return nil, fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	if ctx == nil {
		ctx = context.Background()
	}

	// Purge points and local graph state for each gone branch before deleting
	// the git branch itself.
	for _, branch := range gone {
		if err := qdrantClient.DeleteByFilter(ctx, collection, map[string]string{"branch": branch}); err != nil {
			return nil, fmt.Errorf("purging qdrant points for branch %q: %w", branch, err)
		}
		fmt.Printf("Purged qdrant points for branch %q\n", branch)

		if err := db.ResetBranch(project, branch); err != nil {
			return nil, fmt.Errorf("removing local state for branch %q: %w", branch, err)
		}
		fmt.Printf("Removed local graph state for branch %q\n", branch)
	}

	deleteArgs := append([]string{"-C", repoPath, "branch", "-D"}, gone...)
	c := exec.Command("git", deleteArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("git branch -D: %w", err)
	}
	return gone, nil
}

// goneBranches parses `git branch -vv` and returns the local branches whose
// upstream tracking branch has been deleted (marked "[...: gone]").
func goneBranches(repoPath string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoPath, "branch", "-vv").Output()
	if err != nil {
		return nil, fmt.Errorf("git branch -vv: %w", err)
	}

	var gone []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ": gone]") {
			continue
		}
		// Strip the leading "* " marker on the current branch, then take the
		// first whitespace-separated token as the branch name.
		line = strings.TrimPrefix(strings.TrimSpace(line), "* ")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		gone = append(gone, fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing branch list: %w", err)
	}
	return gone, nil
}
