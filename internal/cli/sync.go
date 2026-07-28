package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"nav/internal/services"
)

var (
	syncPath   string
	syncSince  string
	syncDryRun bool
)

var syncCmd = &cobra.Command{
	Use:   "sync [project]",
	Short: "Lazily re-embed what changed since the last sync (or replay commits with --since)",
	Long: "With no flags, nav sync is the fast, idempotent path meant to run on every prompt\n" +
		"(and is exactly what the UserPromptSubmit hook calls): it detects files changed\n" +
		"since the last sync via git status/HEAD movement (or file mtimes outside a git\n" +
		"repo), re-parses only those, and re-embeds only the chunks whose content actually\n" +
		"changed, tracked via the per-branch manifest in .nav/nav-<branch>.db. With nothing\n" +
		"dirty it is a near no-op.\n\n" +
		"--since switches to the older commit-log replay mode: it walks `git log` for\n" +
		"commits after the given date (or hash/ref) and unconditionally re-indexes every\n" +
		"file they touched, ignoring the manifest. This is for catching up commits made\n" +
		"with NAV_SKIP set (nav's --no-verify equivalent), not routine use.\n\n" +
		"Both the project name and --path are optional. When the project name is\n" +
		"omitted it defaults to the basename of the current directory; when --path is\n" +
		"omitted the path defaults to the project's registered path, or the current\n" +
		"directory.",
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
	syncCmd.Flags().StringVar(&syncPath, "path", "", "Path to the repository root (default: project path or current directory)")
	syncCmd.Flags().StringVar(&syncSince, "since", "", "Switch to commit-log replay mode: only consider commits after this date (e.g. 2024-01-01)")
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Print what would change without doing it")
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	project, path, err := services.ResolveProject(args, syncPath)
	if err != nil {
		return err
	}

	if syncSince == "" {
		result, err := services.LazySync(ctx, project, path, syncDryRun)
		if err != nil {
			return err
		}
		fmt.Println(result.Summary())
		return nil
	}

	result, err := services.SyncSince(ctx, project, path, syncSince, syncDryRun)
	if err != nil {
		return err
	}

	if result.CommitCount == 0 {
		fmt.Println("No commits found.")
		return nil
	}
	if len(result.ChangedFiles) == 0 {
		fmt.Println("No changed files detected.")
		return nil
	}
	if syncDryRun {
		fmt.Printf("Files that would be re-indexed (%d):\n", len(result.ChangedFiles))
		for _, f := range result.ChangedFiles {
			fmt.Printf("  %s\n", f)
		}
	}
	return nil
}
