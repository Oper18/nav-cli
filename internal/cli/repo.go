package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"nav/config"
	"nav/internal/db"
	"nav/internal/services"
)

// ---------------------------------------------------------------------------
// Top-level repo command
// ---------------------------------------------------------------------------

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Git repository helpers",
}

// ---------------------------------------------------------------------------
// repo fetch
// ---------------------------------------------------------------------------

var repoFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch all remotes and prune deleted refs (git fetch --all --prune)",
	RunE:  runRepoFetch,
}

func runRepoFetch(cmd *cobra.Command, args []string) error {
	c := exec.Command("git", "fetch", "--all", "--prune")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("git fetch --all --prune: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// repo clean-branches
// ---------------------------------------------------------------------------

var (
	repoCleanCollection string
	repoCleanPath       string
)

var repoCleanBranchesCmd = &cobra.Command{
	Use:   "clean-branches [project]",
	Short: "Delete local branches whose upstream is gone and purge their points from Qdrant",
	Long: "Delete local branches whose upstream is gone and purge their points from Qdrant.\n\n" +
		"Both the project name and --path are optional. When the project name is\n" +
		"omitted it defaults to the basename of the current directory; when --path is\n" +
		"omitted the path defaults to the project's registered path, or the current\n" +
		"directory.",
	Args: cobra.MaximumNArgs(1),
	RunE: runRepoCleanBranches,
}

func init() {
	repoCleanBranchesCmd.Flags().StringVar(&repoCleanCollection, "collection", "", "Qdrant collection name (default: nav_<project>)")
	repoCleanBranchesCmd.Flags().StringVar(&repoCleanPath, "path", "", "Path to the repository root (default: project path or current directory)")
}

func runRepoCleanBranches(cmd *cobra.Command, args []string) error {
	repoCleanProject, repoPath, err := resolveProject(args, repoCleanPath)
	if err != nil {
		return err
	}

	out, err := exec.Command("git", "-C", repoPath, "branch", "-vv").Output()
	if err != nil {
		return fmt.Errorf("git branch -vv: %w", err)
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
		return fmt.Errorf("parsing branch list: %w", err)
	}

	if len(gone) == 0 {
		fmt.Println("No branches with gone upstream.")
		return nil
	}

	// Resolve config and connect to Qdrant.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	collection := repoCleanCollection
	if collection == "" {
		collection = "nav_" + repoCleanProject
	}

	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Purge points for each gone branch before deleting the git branch.
	for _, branch := range gone {
		if err := qdrantClient.DeleteByFilter(ctx, collection, map[string]string{"branch": branch}); err != nil {
			return fmt.Errorf("purging qdrant points for branch %q: %w", branch, err)
		}
		fmt.Printf("Purged qdrant points for branch %q\n", branch)
	}

	deleteArgs := append([]string{"-C", repoPath, "branch", "-D"}, gone...)
	c := exec.Command("git", deleteArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("git branch -D: %w", err)
	}
	return nil
}
