package services

import (
	"context"
	"fmt"
	"sort"
	"time"

	"nav/config"
	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/llm"
)

// SearchOptions configures a semantic search against a project's indexed
// symbols.
type SearchOptions struct {
	Query string
	// Branch filters by an exact git branch; "" means no branch filter. It
	// is ignored when BranchChain is set.
	Branch string
	// BranchChain, when non-empty, searches every listed branch's points in
	// one query (most-preferred branch first — normally the current branch
	// followed by its ancestor chain, see BranchChain()) instead of a single
	// exact-match Branch filter. When two branches in the chain both hold a
	// point for the same file+symbol, the one earlier in the chain wins.
	BranchChain []string
	Type        string // filter by symbol type; "" means no filter
	Lang        string // filter by language; "" means no filter
	Threshold   float64
	Collection  string // Qdrant collection name; "" defaults to "nav_<project>"
	Top         int
}

// BranchChain resolves branch's ancestor chain for search fallback: branch
// itself, then whichever branch it was bootstrapped from (metaParentBranch,
// set once by detectParentBranch on that branch's first sync), then that
// branch's own parent, and so on. It stops at the first branch with no
// recorded parent, a branch nav has no database for, or a cycle/depth cap
// (maxParentChainDepth), so a symbol never re-embedded on branch is still
// found via whichever ancestor last held it.
func BranchChain(repoPath, branch string) ([]string, error) {
	chain := []string{branch}
	if branch == "" {
		return chain, nil
	}
	seen := map[string]bool{branch: true}
	cur := branch
	for i := 0; i < maxParentChainDepth; i++ {
		if !db.Exists(repoPath, cur) {
			break
		}
		sdb, err := db.Open(repoPath, cur)
		if err != nil {
			return chain, err
		}
		parent, ok, metaErr := sdb.GetMeta(metaParentBranch)
		sdb.Close()
		if metaErr != nil {
			return chain, metaErr
		}
		if !ok || parent == "" || seen[parent] {
			break
		}
		chain = append(chain, parent)
		seen[parent] = true
		cur = parent
	}
	return chain, nil
}

// Search embeds the query, searches Qdrant, and returns at most Top distinct
// symbol hits (chunks of the same symbol are collapsed to their
// best-scoring chunk).
func Search(ctx context.Context, project string, opts SearchOptions) ([]qdrant.Hit, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}

	llmClient := llm.NewClient(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
		time.Duration(cfg.LLM.RequestTimeout)*time.Second, time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

	vecs, err := llmClient.EmbedQuery(ctx, cfg.Embedding.Model, cfg.Embedding.QueryInstruction, []string{opts.Query})
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedder returned no vectors")
	}
	queryVec := vecs[0]

	filters := map[string]string{}
	var branchIn []string
	switch {
	case len(opts.BranchChain) > 0:
		branchIn = opts.BranchChain
	case opts.Branch != "":
		filters["branch"] = opts.Branch
	}
	if opts.Type != "" {
		filters["type"] = opts.Type
	}
	if opts.Lang != "" {
		filters["language"] = opts.Lang
	}

	collection := opts.Collection
	if collection == "" {
		collection = "nav_" + project
	}

	if err := EnsureLocalQdrant(cfg); err != nil {
		return nil, fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return nil, fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	results, err := qdrantClient.Search(ctx, collection, queryVec, overFetch(opts.Top, len(branchIn)), opts.Threshold, filters, branchIn)
	if err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}
	return topN(collapseChunks(results, branchChainRank(opts.BranchChain)), opts.Top), nil
}

// chunkFanout is how many extra candidates to request before collapsing chunks
// of the same symbol, so a few large multi-chunk symbols cannot push distinct
// results out of the requested top-K.
const chunkFanout = 4

// overFetch scales a requested result count up so there is room to collapse
// chunks of the same symbol back down to count distinct hits. branches is the
// number of branches being searched at once (0 or 1 for a single-branch/
// unfiltered search); merging more branches' results needs proportionally
// more raw candidates before collapsing.
func overFetch(count, branches int) int {
	if count <= 0 {
		return count
	}
	if branches < 1 {
		branches = 1
	}
	return count * chunkFanout * branches
}

// branchChainRank maps each branch in chain to its position (0 = most
// preferred), for use by collapseChunks. A nil/empty chain yields a nil map,
// which collapseChunks treats as "no branch preference, highest score wins".
func branchChainRank(chain []string) map[string]int {
	if len(chain) == 0 {
		return nil
	}
	rank := make(map[string]int, len(chain))
	for i, b := range chain {
		if _, ok := rank[b]; !ok {
			rank[b] = i
		}
	}
	return rank
}

// collapseChunks deduplicates hits belonging to the same underlying source
// symbol (same file + symbol name — the same symbol can appear under several
// chunk numbers, and, when searching a branch chain, under several branches
// at once), keeping one representative hit per (file, symbol). When rank is
// non-nil, the hit from the most-preferred branch wins regardless of score
// (so a symbol re-embedded on the current branch always wins over the same
// symbol inherited from an ancestor branch); ties, and everything when rank
// is nil, are broken by score. The result is re-sorted by score descending,
// matching the order Search's callers expect.
func collapseChunks(hits []qdrant.Hit, rank map[string]int) []qdrant.Hit {
	type entry struct {
		hit  qdrant.Hit
		rank int
	}
	best := make(map[[2]string]entry, len(hits))
	order := make([][2]string, 0, len(hits))
	for _, h := range hits {
		key := [2]string{h.Payload.FilePath, h.Payload.Symbol}
		r, ok := rank[h.Payload.Branch]
		if !ok {
			r = len(rank) // unranked (or no chain given): lowest priority, score decides
		}
		cur, exists := best[key]
		if !exists {
			best[key] = entry{hit: h, rank: r}
			order = append(order, key)
			continue
		}
		if r < cur.rank || (r == cur.rank && h.Score > cur.hit.Score) {
			best[key] = entry{hit: h, rank: r}
		}
	}

	out := make([]qdrant.Hit, 0, len(order))
	for _, key := range order {
		out = append(out, best[key].hit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// topN returns at most n hits. A non-positive n returns hits unchanged.
func topN(hits []qdrant.Hit, n int) []qdrant.Hit {
	if n > 0 && len(hits) > n {
		return hits[:n]
	}
	return hits
}
