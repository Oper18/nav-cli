package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"nav/config"
	"nav/internal/services"
)

var (
	deletePath       string
	deleteCollection string
	deleteYes        bool
)

var deleteCmd = &cobra.Command{
	Use:   "delete [project]",
	Short: "Permanently delete a project: its Qdrant collection, local index state, hooks, and registration",
	Long: "Permanently delete a project: its Qdrant collection, all local SQLite index\n" +
		"state (chunk manifest + knowledge graph, every branch), its generated\n" +
		"README, its entry in ~/.nav-cli/projects.yaml, and — when the project's\n" +
		"repository is known — every local hook nav installed there (git\n" +
		"pre-commit/post-merge/post-rewrite/reference-transaction, plus the\n" +
		"Claude/Qwen/Cursor/OpenCode prompt hooks), if any exist.\n\n" +
		"When no project name is given, the project is resolved from --path (or the\n" +
		"current directory) by matching it against the registered path in\n" +
		"projects.yaml — the same repo-path lookup nav's git hooks use to find a\n" +
		"repo's own project — rather than just the directory's basename, so `nav\n" +
		"delete` run from inside a repo finds the project actually registered for\n" +
		"it even when the name differs. An explicit project name is used as-is and\n" +
		"skips path resolution entirely.\n\n" +
		"This cannot be undone. Pass --yes to skip the confirmation prompt.",
	Args: cobra.MaximumNArgs(1),
	RunE: runDelete,
}

func init() {
	deleteCmd.Flags().StringVar(&deletePath, "path", "", "Path to resolve the project from when no project name is given (default: current directory)")
	deleteCmd.Flags().StringVar(&deleteCollection, "collection", "", "Qdrant collection name (default: nav_<project>)")
	deleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "Delete without prompting for confirmation")
}

func runDelete(cmd *cobra.Command, args []string) error {
	project, repoPath, err := resolveDeleteTarget(args)
	if err != nil {
		return err
	}

	collection := deleteCollection
	if collection == "" {
		collection = "nav_" + project
	}

	if !deleteYes {
		confirmed, err := confirmDelete(project, collection, repoPath)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Aborted; nothing was deleted.")
			return nil
		}
	}

	if err := services.DeleteProject(cmd.Context(), project, repoPath, collection); err != nil {
		return err
	}
	fmt.Printf("Deleted project %q (collection %q).\n", project, collection)
	return nil
}

// resolveDeleteTarget determines which project to delete and its known
// repository path (used only to also clean up a legacy in-repo .nav/
// directory — "" when unknown).
//
// An explicit project name is used as-is; its registered path, if any, is
// looked up but not required. With no project name, the target is resolved
// by matching --path (or the current directory) against projects.yaml —
// deliberately via a read-only lookup (config.FindProjectByPath), not
// services.ResolveProject, which would register a *new* entry for an
// unmatched path. A delete command must never have the side effect of
// creating the very registration it's about to remove; an unmatched path is
// an error, not a fallback to "delete something named after this directory
// whether or not it was ever indexed."
func resolveDeleteTarget(args []string) (project, repoPath string, err error) {
	if len(args) > 0 && args[0] != "" {
		project = args[0]
		if proj, ok := config.FindProject(project); ok {
			repoPath = proj.Path
		}
		return project, repoPath, nil
	}

	path := deletePath
	if path == "" {
		path, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("determining current directory: %w", err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolving path %q: %w", path, err)
	}

	if proj, ok := config.FindProjectByPath(abs); ok {
		return proj.Name, proj.Path, nil
	}
	return "", "", fmt.Errorf("no project registered for %s; pass a project name explicitly (nav delete <project>)", abs)
}

// confirmDelete asks for confirmation before an irreversible delete. Empty
// input (including immediate EOF on non-interactive stdin) defaults to "no".
func confirmDelete(project, collection, repoPath string) (bool, error) {
	hooksNote := ""
	if repoPath != "" {
		hooksNote = fmt.Sprintf(", and any nav hooks installed in %s", repoPath)
	}
	fmt.Printf("This will permanently delete project %q: its Qdrant collection %q,\n"+
		"all local index state, its projects.yaml entry%s. This cannot be undone. [y/N]: ", project, collection, hooksNote)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}
