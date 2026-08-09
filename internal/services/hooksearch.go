package services

import (
	"context"
	"fmt"
	"os"

	"nav/internal/hook"
)

// SyncBeforeSearch runs the lazy sync in-process (not as a separate `nav
// sync` subprocess) so the query embedding/search that follows always sees a
// fresh index, without paying for a second process spawn on the hook's
// latency budget. Sync's own summary line — and any error — goes to stderr,
// never stdout: stdout is reserved for the <nav-context> block the assistant
// injects verbatim.
func SyncBeforeSearch(ctx context.Context, project, path string) {
	result, err := LazySync(ctx, project, path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: sync: %v\n", err)
		return
	}
	if result.Skipped {
		return
	}
	if result.ChunksEmbedded > 0 || result.ChunksRemoved > 0 {
		fmt.Fprintln(os.Stderr, result.Summary())
	}
}

// GitHookSync runs a lazy sync against repoPath for the git pre-commit/
// post-merge hooks, which carry no project flag (git invokes them with a
// fixed argument list it doesn't control). The project is resolved from
// repoPath itself via ResolveProjectByPath, so each repo's git-triggered
// syncs land in that repo's own project/collection — the one its `nav
// index`/assistant hooks actually search — instead of a single "default"
// bucket shared (and mixed together) across every repo with nav's git hooks
// installed.
func GitHookSync(repoPath string) (LazySyncResult, error) {
	project := ResolveProjectByPath(repoPath)
	return LazySync(context.Background(), project, repoPath, false)
}

// HookSearch is the shared core of every AI-assistant prompt hook (Claude
// Code, Qwen Code, Cursor, OpenCode): it syncs the index in-process, then
// searches the knowledge graph before it searches anything else. Any
// identifier mentioned verbatim in query (e.g. a symbol name) is looked up
// as an exact match against the current branch's SQLite graph via
// GraphSearch — cheap, precise, and it carries real call-graph relationships
// that a vector chunk alone doesn't. Only once that's exhausted does it fall
// back to embedding query and searching Qdrant under project's collection —
// across the current branch's chain of ancestor branches (BranchChain), so
// symbols this branch never re-embedded itself are still found via whichever
// ancestor last held them — to fill whatever's left of topK, skipping any
// symbol the graph lookup already returned. Results are ContextResult
// entries ready for hook.FormatContextBlock, graph hits first. An empty
// query is a no-op.
func HookSearch(ctx context.Context, project, path, query string, topK int, minScore float64) ([]hook.ContextResult, error) {
	if query == "" {
		return nil, nil
	}

	SyncBeforeSearch(ctx, project, path)

	graphHits, err := GraphSearch(project, path, query, topK)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: graph search: %v\n", err)
	}
	if topK > 0 && len(graphHits) >= topK {
		return graphHits, nil
	}

	chain, err := BranchChain(project, path, CurrentBranch(path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: resolving branch chain: %v\n", err)
	}

	semanticTop := topK
	if topK > 0 {
		semanticTop = topK - len(graphHits)
	}
	hits, err := Search(ctx, project, SearchOptions{Query: query, Threshold: minScore, Top: semanticTop, BranchChain: chain})
	if err != nil {
		if len(graphHits) > 0 {
			return graphHits, nil // the graph already found something; don't drop it over a Qdrant error
		}
		return nil, err
	}

	seen := make(map[[2]string]bool, len(graphHits))
	for _, r := range graphHits {
		seen[[2]string{r.File, r.Symbol}] = true
	}

	ctxResults := append([]hook.ContextResult(nil), graphHits...)
	for _, r := range hits {
		key := [2]string{r.Payload.FilePath, r.Payload.Symbol}
		if seen[key] {
			continue // already surfaced by the exact graph lookup
		}
		seen[key] = true
		ctxResults = append(ctxResults, hook.ContextResult{
			Score:   r.Score,
			Symbol:  r.Payload.Symbol,
			Type:    r.Payload.Type,
			File:    r.Payload.FilePath,
			Purpose: r.Payload.Summary,
			Code:    r.Payload.Content,
		})
	}
	return ctxResults, nil
}
