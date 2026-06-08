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
	indexIgnoreDirs  []string
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
	indexCmd.Flags().StringSliceVar(&indexIgnoreDirs, "ignore-dir", []string{}, "Directories to exclude from indexing (can be specified multiple times)")
}

func runIndex(cmd *cobra.Command, args []string) error {
	project, path, err := resolveProject(args, indexPath)
	if err != nil {
		return err
	}
	return indexFiles(cmd.Context(), project, path, indexCollection, indexLang, indexConcurrency, indexDryRun, indexIgnoreDirs)
}

// indexFiles contains the shared indexing logic used by both `nav index` and
// `nav sync`. When filesToProcess is nil all files under repoPath are walked.
func indexFiles(
	ctx context.Context,
	project, repoPath, collectionFlag, langFilter string,
	concurrency int,
	dryRun bool,
	ignoreDirs []string,
) error {
	return indexSpecificFiles(ctx, project, repoPath, collectionFlag, langFilter, concurrency, dryRun, nil, ignoreDirs)
}

// indexSpecificFiles indexes only the given relative file paths (or all files
// when specificFiles is nil).
func indexSpecificFiles(
	ctx context.Context,
	project, repoPath, collectionFlag, langFilter string,
	concurrency int,
	dryRun bool,
	specificFiles []string,
	ignoreDirs []string,
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

			// Convert path to relative path to check against ignore directories
			rel, err := filepath.Rel(repoPath, path)
			if err != nil {
				return nil // skip entries that can't be relativized
			}

			// Handle directory skipping based on ignore dirs
			if d.IsDir() {
				// Clean relative path for comparison - this represents the current directory path relative to repo root
				cleanRelPath := filepath.Clean(rel)
				
				// Compare against ignore directories provided via the flag
				for _, ignoreDir := range ignoreDirs {
					if filepath.IsAbs(ignoreDir) {
						// If ignoreDir is absolute, check if the current absolute path starts with it
						if strings.HasPrefix(path, ignoreDir+string(filepath.Separator)) || path == ignoreDir {
							return filepath.SkipDir
						}
					} else {
						// If ignoreDir is relative, treat it as relative to the repo root
						// cleanRelPath is the current directory path relative to the repo root
						// For example: if we're looking at /repo/src/utils and ignoreDir="vendor", 
						// we check if "src/utils" matches the pattern "vendor"  
						normalizedIgnoreDir := filepath.Clean(ignoreDir)
						if cleanRelPath == normalizedIgnoreDir ||
						   strings.HasPrefix(cleanRelPath, normalizedIgnoreDir+string(filepath.Separator)) {
							return filepath.SkipDir
						}
					}
				}
				return nil // Don't add directories to relPaths, continue walking
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

	// 6. Build LLM client.
	llmClient := llm.NewClientWithEmbedTimeout(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
		time.Duration(cfg.LLM.RequestTimeout)*time.Second, 
		time.Duration(cfg.Embedding.RequestTimeout)*time.Second,
		time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

	// 6b. Establish the project README *before* summarising, so each symbol
	// summary can be grounded in the project's overall purpose. A full index
	// regenerates it from the project's source; an incremental sync reuses the
	// README produced by the last full index. A missing/failed README is
	// non-fatal — summaries simply proceed without project context.
	var projectReadme string
	if specificFiles == nil {
		readme, err := buildAndWriteReadme(ctx, llmClient, cfg.LLM.ReadmeModel, project, allSymbols)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: generating readme: %v\n", err)
		} else {
			projectReadme = readme
			fmt.Printf("Wrote project readme to %s\n", config.ProjectReadmePath(project))
		}
	} else {
		readme, err := config.ReadProjectReadme(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: reading project readme: %v\n", err)
		}
		projectReadme = readme
	}

	// 6c. Summarise symbols, passing the README (capped) as shared context.
	readmeContext := capRunes(projectReadme, readmeContextCap)
	requests := make([]llm.SummariseRequest, len(allSymbols))
	for i, sym := range allSymbols {
		requests[i] = llm.SummariseRequest{
			Language:       string(sym.Language),
			Symbol:         sym.Symbol,
			Type:           sym.Type,
			Content:        sym.Content,
			ProjectContext: readmeContext,
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

	// 8. Build embedding inputs and embed in batches of 20 via OpenRouter. A
	// single input that exceeds the embedding model's token limit makes the whole
	// batch fail with HTTP 400, so oversized symbols (typically very large
	// functions or files) are split into several chunks that each fit a
	// conservative character budget. Each chunk becomes its own point; chunks of
	// the same symbol share (branch, symbol) and are ordered by chunk_number.
	budget := embedCharBudget(cfg.Embedding.MaxTokens)

	// chunkRef ties an entry in texts/vectors back to its source symbol and its
	// position within that symbol.
	type chunkRef struct {
		symIdx  int
		content string // the slice of code stored in this chunk's payload
		number  int
		count   int
	}
	var texts []string
	var refs []chunkRef
	split := 0
	for i, sym := range allSymbols {
		chunks := qdrant.BuildEmbedChunks(sym.Payload, budget)
		if len(chunks) > 1 {
			split++
		}
		for n, ch := range chunks {
			texts = append(texts, ch.Text)
			refs = append(refs, chunkRef{symIdx: i, content: ch.Content, number: n, count: len(chunks)})
		}
	}
	if split > 0 {
		fmt.Fprintf(os.Stderr, "note: split %d oversized symbol(s) into multiple chunks to fit the embedding token limit\n", split)
	}

	const embedBatch = 20
	vectors := make([][]float32, len(texts))

	fmt.Printf("Embedding %d chunks", len(texts))
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

	// 11b. On an incremental re-index, purge every existing point for the files
	// being re-indexed before upserting. A symbol may now span a different number
	// of chunks than before (or have been removed), and deterministic IDs only
	// overwrite chunks that still exist — so without this, shrinking a symbol from
	// N chunks to M<N would leave orphaned chunks behind.
	if specificFiles != nil {
		for rel := range fileSet {
			if err := qdrantClient.DeleteByFilter(ctx, collection, map[string]string{
				"branch":    branch,
				"file_path": rel,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warn: clearing old points for %s: %v\n", rel, err)
			}
		}
	}

	// 12. Build Points (one per chunk) and upsert.
	points := make([]qdrant.Point, len(texts))
	for i, ref := range refs {
		sym := allSymbols[ref.symIdx]
		payload := sym.Payload
		payload.Content = ref.content
		payload.ChunkNumber = ref.number
		payload.ChunkCount = ref.count
		points[i] = qdrant.Point{
			ID:      qdrant.ID(sym.Branch, sym.Symbol, ref.number),
			Vector:  vectors[i],
			Payload: payload,
		}
	}

	// Upsert in batches to avoid overly large requests.
	const upsertBatch = 100
	fmt.Printf("Upserting %d chunks", len(points))
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

	return nil
}

// Embedding inputs are sized against a character budget derived from the model's
// token limit. We cannot cheaply tokenise here, so we assume a conservative
// chars-per-token ratio and only use a fraction of the limit; this keeps even
// densely-tokenised source comfortably under the cap. Symbols whose rendered
// text exceeds the budget are split into multiple chunks rather than truncated.
const (
	embedCharsPerToken = 3.0
	embedSafetyFactor  = 0.8
)

// embedCharBudget converts a max-token limit into a conservative maximum rune
// count for an embedding input. A non-positive limit falls back to 8192.
func embedCharBudget(maxTokens int) int {
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	return int(float64(maxTokens) * embedCharsPerToken * embedSafetyFactor)
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

// readmeContextCap bounds how much of the project README is injected into each
// per-symbol summary prompt. The README is shared by every summary call, so an
// unbounded copy would multiply token cost across thousands of requests.
const readmeContextCap = 8000

// readmeSourceBudget bounds the amount of project source fed to the README
// generator in a single request, keeping it under the model's context window.
const readmeSourceBudget = 120000

// buildAndWriteReadme generates a business-logic-focused README from the
// project's source code (not from per-symbol summaries, which do not exist yet)
// and writes it to ~/.nav-cli/projects/<project>/readme.md. It returns the
// generated markdown so it can be reused as summarisation context.
func buildAndWriteReadme(ctx context.Context, client *llm.Client, readmeModel, project string, symbols []parser.Symbol) (string, error) {
	langSeen := make(map[string]bool)
	var languages []string
	for _, sym := range symbols {
		if lang := string(sym.Language); lang != "" && !langSeen[lang] {
			langSeen[lang] = true
			languages = append(languages, lang)
		}
	}

	source, truncated := buildReadmeSource(symbols, readmeSourceBudget)
	if truncated {
		fmt.Fprintf(os.Stderr, "note: project source exceeds the readme budget; generating from the first %d chars\n", readmeSourceBudget)
	}

	fmt.Print("Generating project readme")
	content, err := client.GenerateReadme(ctx, readmeModel, llm.ReadmeRequest{
		Project:   project,
		Languages: languages,
		Source:    source,
	})
	if err != nil {
		fmt.Println()
		return "", err
	}
	fmt.Println(" done")

	readme := strings.TrimSpace(content) + "\n"
	if err := config.WriteProjectReadme(project, readme); err != nil {
		return "", err
	}
	return readme, nil
}

// buildReadmeSource concatenates the indexed symbols' code into a single
// evidence blob for README generation, grouped by file, stopping once budget
// bytes are reached. The second return value reports whether the cap truncated
// the project.
func buildReadmeSource(symbols []parser.Symbol, budget int) (string, bool) {
	var b strings.Builder
	for _, sym := range symbols {
		section := fmt.Sprintf("// %s — %s (%s)\n%s\n\n", sym.FilePath, sym.Symbol, sym.Type, sym.Content)
		if budget > 0 && b.Len() > 0 && b.Len()+len(section) > budget {
			return b.String(), true
		}
		b.WriteString(section)
	}
	return b.String(), false
}

// capRunes returns s truncated to at most max runes, cutting on a rune boundary.
// A non-positive max returns s unchanged.
func capRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
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
