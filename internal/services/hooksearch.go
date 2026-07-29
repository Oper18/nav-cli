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

// GitHookSync runs a lazy sync against repoPath under the default project
// name, for the git pre-commit/post-merge hooks (which carry no project
// flag).
func GitHookSync(repoPath string) (LazySyncResult, error) {
	return LazySync(context.Background(), "default", repoPath, false)
}

// HookSearch is the shared core of every AI-assistant prompt hook (Claude
// Code, Qwen Code, Cursor, OpenCode): it syncs the index in-process, embeds
// query, searches Qdrant under project's collection — across the current
// branch's chain of ancestor branches (BranchChain), so symbols this branch
// never re-embedded itself are still found via whichever ancestor last held
// them — with no type/lang filtering, and returns the collapsed top-topK
// hits as ContextResult entries ready for hook.FormatContextBlock. An empty
// query is a no-op.
func HookSearch(ctx context.Context, project, path, query string, topK int, minScore float64) ([]hook.ContextResult, error) {
	if query == "" {
		return nil, nil
	}

	SyncBeforeSearch(ctx, project, path)

	chain, err := BranchChain(path, CurrentBranch(path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: resolving branch chain: %v\n", err)
	}

	hits, err := Search(ctx, project, SearchOptions{Query: query, Threshold: minScore, Top: topK, BranchChain: chain})
	if err != nil {
		return nil, err
	}

	ctxResults := make([]hook.ContextResult, 0, len(hits))
	for _, r := range hits {
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
