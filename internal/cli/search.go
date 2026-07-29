package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"nav/internal/db/qdrant"
	"nav/internal/services"
)

var (
	searchBranch     string
	searchPath       string
	searchTop        int
	searchType       string
	searchLang       string
	searchJSON       bool
	searchThreshold  float64
	searchCollection string
)

var searchCmd = &cobra.Command{
	Use:   "search <query> [project]",
	Short: "Search indexed symbols by semantic similarity",
	Long: "Search indexed symbols by semantic similarity.\n\n" +
		"Both the project name and --path are optional. When the project name is\n" +
		"omitted it defaults to the basename of the current directory; when --path is\n" +
		"omitted the path defaults to the project's registered path, or the current\n" +
		"directory. When --branch is not given, results are pulled from the current\n" +
		"branch of that repository plus its chain of ancestor branches (whichever\n" +
		"branches its embeddings were bootstrapped from), so symbols never re-embedded\n" +
		"on this branch are still found.",
	Args: cobra.RangeArgs(1, 2),
	RunE: runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&searchBranch, "branch", "", "Filter by an exact git branch (default: current branch + its ancestor chain)")
	searchCmd.Flags().StringVar(&searchPath, "path", "", "Path to the repository root (default: project path or current directory)")
	searchCmd.Flags().IntVar(&searchTop, "top", 5, "Number of results to return")
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by symbol type (function, method, class, ...)")
	searchCmd.Flags().StringVar(&searchLang, "lang", "", "Filter by language")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Output results as JSON")
	searchCmd.Flags().Float64Var(&searchThreshold, "threshold", 0.70, "Minimum similarity score")
	searchCmd.Flags().StringVar(&searchCollection, "collection", "", "Qdrant collection name (default: nav_<project>)")
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	project, repoPath, err := services.ResolveProject(args[1:], searchPath)
	if err != nil {
		return err
	}

	opts := services.SearchOptions{
		Query:      query,
		Type:       searchType,
		Lang:       searchLang,
		Threshold:  searchThreshold,
		Collection: searchCollection,
		Top:        searchTop,
	}
	if searchBranch != "" {
		// An explicit --branch means exactly that branch, not its ancestor chain.
		opts.Branch = searchBranch
	} else {
		chain, err := services.BranchChain(repoPath, services.CurrentBranch(repoPath))
		if err != nil {
			return fmt.Errorf("resolving branch chain: %w", err)
		}
		opts.BranchChain = chain
	}

	results, err := services.Search(cmd.Context(), project, opts)
	if err != nil {
		return err
	}

	if searchJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	printSearchResults(cmd, results)
	return nil
}

func printSearchResults(cmd *cobra.Command, results []qdrant.Hit) {
	w := cmd.OutOrStdout()

	if len(results) == 0 {
		fmt.Fprintln(w, "No results found.")
		return
	}

	const divider = "────────────────────────────────────────────────────"

	for i, r := range results {
		p := r.Payload

		fmt.Fprintf(w, "─── Result %d (score: %.2f) %s\n", i+1, r.Score, divider[:len(divider)-len(fmt.Sprintf("─── Result %d (score: %.2f) ", i+1, r.Score))])
		fmt.Fprintf(w, "Symbol:  %s\n", p.Symbol)
		fmt.Fprintf(w, "Type:    %s\n", p.Type)
		fmt.Fprintf(w, "File:    %s\n", p.FilePath)
		if p.Branch != "" {
			fmt.Fprintf(w, "Branch:  %s\n", p.Branch)
		}
		if p.ChunkCount > 1 {
			fmt.Fprintf(w, "Chunk:   %d/%d (best-matching fragment)\n", p.ChunkNumber+1, p.ChunkCount)
		}

		if p.Summary != "" {
			fmt.Fprintf(w, "\nPurpose:\n%s\n", p.Summary)
		}

		if p.Content != "" {
			lines := strings.SplitN(p.Content, "\n", 22)
			truncated := false
			if len(lines) > 20 {
				lines = lines[:20]
				truncated = true
			}
			fmt.Fprintf(w, "\nCode:\n%s\n", strings.Join(lines, "\n"))
			if truncated {
				fmt.Fprintln(w, "... (truncated)")
			}
		}

		fmt.Fprintln(w, divider)
	}
}
