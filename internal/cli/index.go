package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"nav/config"
	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/llm"
	"nav/internal/parser"
	"nav/internal/services"
)

var (
	indexPath        string
	indexConcurrency int
	indexDryRun      bool
	indexForce       bool
	indexLang        string
	indexCollection  string
)

var indexCmd = &cobra.Command{
	Use:   "index [project]",
	Short: "Index a repository into Qdrant",
	Long: "Index a repository into Qdrant.\n\n" +
		"Both the project name and --path are optional. When the project name is\n" +
		"omitted it defaults to the basename of the current directory; when --path is\n" +
		"omitted the path defaults to the project's registered path, or the current\n" +
		"directory.",
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
}

func runIndex(cmd *cobra.Command, args []string) error {
	project, path, err := resolveProject(args, indexPath)
	if err != nil {
		return err
	}
	return indexFiles(cmd.Context(), project, path, indexCollection, indexLang, indexConcurrency, indexDryRun)
}

// indexFiles contains the shared indexing logic used by both `nav index` and
// `nav sync`. When filesToProcess is nil all files under repoPath are walked.
func indexFiles(
	ctx context.Context,
	project, repoPath, collectionFlag, langFilter string,
	concurrency int,
	dryRun bool,
) error {
	return indexSpecificFiles(ctx, project, repoPath, collectionFlag, langFilter, concurrency, dryRun, nil)
}

// indexSpecificFiles indexes only the given relative file paths (or all files
// when specificFiles is nil).
func indexSpecificFiles(
	ctx context.Context,
	project, repoPath, collectionFlag, langFilter string,
	concurrency int,
	dryRun bool,
	specificFiles []string,
) error {
	// 1. Load config and credentials.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	// 2. Collect files to process.
	var relPaths []string

	if specificFiles != nil {
		relPaths = specificFiles
	} else {
		if err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(repoPath, path)
			if err != nil {
				return nil
			}
			relPaths = append(relPaths, rel)
			return nil
		}); err != nil {
			return fmt.Errorf("walking repository: %w", err)
		}
	}

	// 3. Filter files.
	var toProcess []string
	for _, rel := range relPaths {
		if parser.ShouldSkip(rel, cfg.Indexing.SkipPatterns) {
			continue
		}
		lang := parser.DetectLanguage(rel)
		if lang == "" {
			continue
		}
		if langFilter != "" && lang != langFilter {
			continue
		}
		toProcess = append(toProcess, rel)
	}

	// 4. Resolve the current git branch — it's part of every point's ID.
	branch := currentBranch(repoPath)

	// 5. Extract symbols from each file.
	var allSymbols []parser.Symbol
	fileSet := make(map[string]bool)

	for _, rel := range toProcess {
		syms, err := parser.ExtractSymbols(ctx, repoPath, rel, branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: extract %s: %v\n", rel, err)
			continue
		}
		if len(syms) > 0 {
			fileSet[rel] = true
		}
		allSymbols = append(allSymbols, syms...)
	}

	// 5. Dry-run: print a summary table and return.
	if dryRun {
		fmt.Printf("%-60s  %-30s  %s\n", "File", "Symbol", "Type")
		fmt.Println(strings.Repeat("-", 100))
		for _, sym := range allSymbols {
			fmt.Printf("%-60s  %-30s  %s\n", sym.FilePath, sym.Symbol, sym.Type)
		}
		fmt.Printf("\nTotal: %d symbols from %d files\n", len(allSymbols), len(fileSet))
		return nil
	}

	if len(allSymbols) == 0 {
		fmt.Println("No symbols found.")
		return nil
	}

	// 6. Build LLM client and summarise symbols.
	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels)

	requests := make([]llm.SummariseRequest, len(allSymbols))
	for i, sym := range allSymbols {
		requests[i] = llm.SummariseRequest{
			Language: string(sym.Language),
			Symbol:   sym.Symbol,
			Type:     sym.Type,
			Content:  sym.Content,
		}
	}

	fmt.Printf("Summarising %d symbols", len(allSymbols))
	responses, _ := llmClient.SummariseBatch(ctx, requests, concurrency)

	// 7. Apply summaries and the LLM-derived business metadata.
	for i := range allSymbols {
		if i < len(responses) {
			allSymbols[i].Summary = responses[i].Summary
			allSymbols[i].Tags = responses[i].Tags
			allSymbols[i].BusinessContext = responses[i].BusinessContext
			allSymbols[i].Responsibilities = responses[i].Responsibilities
		}
	}
	fmt.Println(" done")

	// 7b. Derive reverse call edges (called_by) across all extracted symbols.
	computeCalledBy(allSymbols)

	// 8. Build text blocks and embed in batches of 20 via OpenRouter.
	texts := make([]string, len(allSymbols))
	for i, sym := range allSymbols {
		texts[i] = qdrant.BuildEmbedText(sym.Payload)
	}

	const embedBatch = 20
	vectors := make([][]float32, len(texts))

	fmt.Printf("Embedding %d symbols", len(texts))
	for start := 0; start < len(texts); start += embedBatch {
		end := start + embedBatch
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		vecs, err := llmClient.Embed(ctx, cfg.Embedding.Model, batch)
		if err != nil {
			return fmt.Errorf("embedding batch [%d:%d]: %w", start, end, err)
		}
		copy(vectors[start:], vecs)

		if (start/embedBatch)%5 == 0 {
			fmt.Print(".")
		}
	}
	fmt.Println(" done")

	// 10. Determine collection name.
	collection := collectionFlag
	if collection == "" {
		collection = "nav_" + project
	}

	// 11. Create Qdrant client and ensure collection exists.
	if err := services.EnsureLocalQdrant(cfg); err != nil {
		return fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()
	if err := qdrantClient.EnsureCollection(ctx, collection, cfg.Embedding.Dimension); err != nil {
		return fmt.Errorf("ensuring collection: %w", err)
	}

	// 12. Build Points and upsert.
	points := make([]qdrant.Point, len(allSymbols))
	for i, sym := range allSymbols {
		points[i] = qdrant.Point{
			ID:      qdrant.ID(sym.Branch, sym.Symbol),
			Vector:  vectors[i],
			Payload: sym.Payload,
		}
	}

	// Upsert in batches to avoid overly large requests.
	const upsertBatch = 100
	fmt.Printf("Upserting %d symbols", len(points))
	processed := 0
	for start := 0; start < len(points); start += upsertBatch {
		end := start + upsertBatch
		if end > len(points) {
			end = len(points)
		}
		if err := qdrantClient.Upsert(ctx, collection, points[start:end]); err != nil {
			return fmt.Errorf("upserting symbols: %w", err)
		}
		processed += end - start
		if processed%10 == 0 || processed == len(points) {
			fmt.Print(".")
		}
	}
	fmt.Println(" done")

	fmt.Printf("Indexed %d symbols from %d files\n", len(allSymbols), len(fileSet))

	// 13. Regenerate the project README on a full index. Incremental syncs
	// (specificFiles != nil) only see a slice of the project and would produce a
	// misleading whole-project document, so they are skipped. README failure is
	// non-fatal: the symbols are already indexed.
	if specificFiles == nil {
		if err := generateProjectReadme(ctx, llmClient, cfg.LLM.ReadmeModel, project, allSymbols); err != nil {
			fmt.Fprintf(os.Stderr, "warn: generating readme: %v\n", err)
		} else {
			fmt.Printf("Wrote project readme to %s\n", config.ProjectReadmePath(project))
		}
	}

	return nil
}

// computeCalledBy populates each symbol's CalledBy with the qualified names of
// the other indexed symbols that call it. A symbol is considered a caller when
// one of its Calls entries matches the callee's fully-qualified name or its bare
// (unqualified) name.
func computeCalledBy(symbols []parser.Symbol) {
	// Map every callable identifier (qualified and bare) to the symbols owning it.
	owners := make(map[string][]int)
	for i, sym := range symbols {
		owners[sym.Symbol] = append(owners[sym.Symbol], i)
		if base := bareName(sym.Symbol); base != sym.Symbol {
			owners[base] = append(owners[base], i)
		}
	}

	// For each caller, attribute every distinct callee back to its owner(s).
	seen := make(map[[2]int]bool)
	for ci, caller := range symbols {
		for _, call := range caller.Calls {
			for _, ti := range owners[call] {
				if ti == ci {
					continue // ignore self-recursion
				}
				key := [2]int{ti, ci}
				if seen[key] {
					continue
				}
				seen[key] = true
				symbols[ti].CalledBy = append(symbols[ti].CalledBy, caller.Symbol)
			}
		}
	}
}

// bareName returns the unqualified portion of a possibly receiver-qualified
// symbol name (e.g. "Client.Close" -> "Close").
func bareName(symbol string) string {
	if idx := strings.LastIndex(symbol, "."); idx >= 0 {
		return symbol[idx+1:]
	}
	return symbol
}

// generateProjectReadme builds a business-logic-focused README from the indexed
// symbols and writes it to ~/.nav-cli/projects/<project>/readme.md.
func generateProjectReadme(ctx context.Context, client *llm.Client, readmeModel, project string, symbols []parser.Symbol) error {
	langSeen := make(map[string]bool)
	var languages []string
	readmeSymbols := make([]llm.ReadmeSymbol, 0, len(symbols))
	for _, sym := range symbols {
		if lang := string(sym.Language); lang != "" && !langSeen[lang] {
			langSeen[lang] = true
			languages = append(languages, lang)
		}
		readmeSymbols = append(readmeSymbols, llm.ReadmeSymbol{
			Symbol:          sym.Symbol,
			FilePath:        sym.FilePath,
			Type:            sym.Type,
			Summary:         sym.Summary,
			BusinessContext: sym.BusinessContext,
		})
	}

	fmt.Print("Generating project readme")
	content, err := client.GenerateReadme(ctx, readmeModel, llm.ReadmeRequest{
		Project:   project,
		Languages: languages,
		Symbols:   readmeSymbols,
	})
	if err != nil {
		fmt.Println()
		return err
	}
	fmt.Println(" done")

	return config.WriteProjectReadme(project, strings.TrimSpace(content)+"\n")
}


// deletedFileIDs queries Qdrant for all points whose branch and file_path match
// any of the given relative paths and returns their IDs for deletion.
func deletedFileIDs(ctx context.Context, qc *db.Client, collection, branch string, deletedPaths []string) ([]string, error) {
	var allIDs []string
	for _, path := range deletedPaths {
		filters := map[string]string{
			"branch":    branch,
			"file_path": path,
		}
		// Use a zero vector to trigger a filter-only scroll (score_threshold=0).
		dummy := make([]float32, 1)
		results, err := qc.Search(ctx, collection, dummy, 1000, 0.0, filters)
		if err != nil {
			// Non-fatal: log and continue.
			fmt.Fprintf(os.Stderr, "warn: querying deleted file %s: %v\n", path, err)
			continue
		}
		for _, r := range results {
			allIDs = append(allIDs, r.ID)
		}
	}
	return allIDs, nil
}

// currentBranch returns the current git branch in repoPath, or "" if it cannot
// be determined (detached HEAD or non-git directory).
func currentBranch(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// spinnerTick prints a dot every 10 symbols as a simple progress indicator.
// Call with index (0-based); returns true when a dot was printed.
func spinnerTick(index int) bool {
	if index > 0 && index%10 == 0 {
		fmt.Print(".")
		return true
	}
	return false
}

// formatDuration formats a duration for display.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
