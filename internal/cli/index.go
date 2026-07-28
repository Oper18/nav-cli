package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"nav/internal/services"
)

var (
	indexPath        string
	indexConcurrency int
	indexDryRun      bool
	indexForce       bool
	indexLang        string
	indexCollection  string
	indexIgnoreDirs  []string
	indexYes         bool
)

var indexCmd = &cobra.Command{
	Use:   "index [project]",
	Short: "Index a repository into Qdrant",
	Long: "Index a repository into Qdrant.\n\n" +
		"Both the project name and --path are optional. When the project name is\n" +
		"omitted it defaults to the basename of the current directory; when --path is\n" +
		"omitted the path defaults to the project's registered path, or the current\n" +
		"directory.\n\n" +
		"When the project already has an index, you are asked whether to replace it;\n" +
		"answering yes deletes its Qdrant collection and local SQLite state (chunk\n" +
		"manifest + knowledge graph) before indexing from scratch. Pass --yes to skip\n" +
		"the prompt and always replace.",
	Args: cobra.MaximumNArgs(1),
	RunE: runIndex,
}

func init() {
	indexCmd.Flags().StringVar(&indexPath, "path", "", "Path to the repository root (default: project path or current directory)")
	indexCmd.Flags().IntVar(&indexConcurrency, "concurrency", 4, "Number of concurrent LLM requests")
	indexCmd.Flags().BoolVar(&indexDryRun, "dry-run", false, "Print symbol summary without indexing")
	indexCmd.Flags().BoolVar(&indexForce, "force", false, "Re-index even if the symbol already exists")
	indexCmd.Flags().StringVar(&indexLang, "lang", "", "Only index files of this language")
	indexCmd.Flags().StringVar(&indexCollection, "collection", "", "Qdrant collection name (default: nav_<project>)")
	indexCmd.Flags().StringSliceVar(&indexIgnoreDirs, "ignore-dir", []string{}, "Directories to exclude from indexing (can be specified multiple times)")
	indexCmd.Flags().BoolVarP(&indexYes, "yes", "y", false, "Replace an existing project's index without prompting for confirmation")
}

func runIndex(cmd *cobra.Command, args []string) error {
	project, path, err := services.ResolveProject(args, indexPath)
	if err != nil {
		return err
	}

	if !indexDryRun {
		collection := indexCollection
		if collection == "" {
			collection = "nav_" + project
		}

		exists, err := services.ProjectExists(cmd.Context(), collection)
		if err != nil {
			return err
		}
		if exists {
			replace := indexYes
			if !replace {
				replace, err = confirmReplaceProject(project)
				if err != nil {
					return err
				}
			}
			if replace {
				if err := services.ResetProject(cmd.Context(), path, collection); err != nil {
					return fmt.Errorf("resetting project: %w", err)
				}
				fmt.Printf("Removed existing index for %q; generating from scratch\n", project)
			} else {
				fmt.Println("Keeping existing index; indexing in place.")
			}
		}
	}

	return services.IndexFiles(cmd.Context(), project, path, indexCollection, indexLang, indexConcurrency, indexDryRun, indexIgnoreDirs)
}

// confirmReplaceProject asks the user whether to wipe and re-index project
// from scratch. Empty input (including immediate EOF on non-interactive
// stdin) defaults to "no".
func confirmReplaceProject(project string) (bool, error) {
	fmt.Printf("Project %q already has an index. Replace it? This deletes the existing\n"+
		"Qdrant collection and local SQLite state before re-indexing from scratch. [y/N]: ", project)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}
