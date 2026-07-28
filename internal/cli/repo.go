package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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
	return services.FetchAll(".")
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
	project, repoPath, err := services.ResolveProject(args, repoCleanPath)
	if err != nil {
		return err
	}

	cleaned, err := services.CleanGoneBranches(cmd.Context(), project, repoPath, repoCleanCollection)
	if err != nil {
		return err
	}
	if len(cleaned) == 0 {
		fmt.Println("No branches with gone upstream.")
	}
	return nil
}
