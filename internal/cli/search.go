package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"nav/config"
	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/llm"
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
		"directory. When --branch is not given, the current branch of that repository\n" +
		"is used as a filter.",
	Args: cobra.RangeArgs(1, 2),
	RunE: runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&searchBranch, "branch", "", "Filter by git branch (default: current branch)")
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

	searchProject, repoPath, err := resolveProject(args[1:], searchPath)
	if err != nil {
		return err
	}

	// Default the branch filter to the current branch of the project's repository.
	branch := searchBranch
	if branch == "" {
		branch = currentBranch(repoPath)
	}

	// 1. Load config and credentials.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	// 2. Embed the query via OpenRouter.
	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels)

	ctx := cmd.Context()
	vecs, err := llmClient.EmbedQuery(ctx, cfg.Embedding.Model, cfg.Embedding.QueryInstruction, []string{query})
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) == 0 {
		return fmt.Errorf("embedder returned no vectors")
	}
	queryVec := vecs[0]

	// 3. Build filters.
	filters := map[string]string{}
	if branch != "" {
		filters["branch"] = branch
	}
	if searchType != "" {
		filters["type"] = searchType
	}
	if searchLang != "" {
		filters["language"] = searchLang
	}

	// 4. Determine collection and search.
	collection := searchCollection
	if collection == "" {
		collection = "nav_" + searchProject
	}

	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()
	results, err := qdrantClient.Search(ctx, collection, queryVec, searchTop, searchThreshold, filters)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}

	// 5. Output.
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
