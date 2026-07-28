package services

import (
	"context"
	"fmt"
	"time"

	"nav/config"
	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/llm"
)

// SearchOptions configures a semantic search against a project's indexed
// symbols.
type SearchOptions struct {
	Query      string
	Branch     string // filter by git branch; "" means no branch filter
	Type       string // filter by symbol type; "" means no filter
	Lang       string // filter by language; "" means no filter
	Threshold  float64
	Collection string // Qdrant collection name; "" defaults to "nav_<project>"
	Top        int
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
	if opts.Branch != "" {
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

	results, err := qdrantClient.Search(ctx, collection, queryVec, overFetch(opts.Top), opts.Threshold, filters)
	if err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}
	return topN(collapseChunks(results), opts.Top), nil
}

// chunkFanout is how many extra candidates to request before collapsing chunks
// of the same symbol, so a few large multi-chunk symbols cannot push distinct
// results out of the requested top-K.
const chunkFanout = 4

// overFetch scales a requested result count up so there is room to collapse
// chunks of the same symbol back down to count distinct hits.
func overFetch(count int) int {
	if count <= 0 {
		return count
	}
	return count * chunkFanout
}

// collapseChunks deduplicates hits belonging to the same symbol (same branch and
// symbol name), keeping the highest-scoring chunk of each. Qdrant returns hits
// by descending score, so the first hit seen for a symbol is its best; input
// order is otherwise preserved.
func collapseChunks(hits []qdrant.Hit) []qdrant.Hit {
	seen := make(map[[2]string]bool, len(hits))
	out := make([]qdrant.Hit, 0, len(hits))
	for _, h := range hits {
		key := [2]string{h.Payload.Branch, h.Payload.Symbol}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}

// topN returns at most n hits. A non-positive n returns hits unchanged.
func topN(hits []qdrant.Hit, n int) []qdrant.Hit {
	if n > 0 && len(hits) > n {
		return hits[:n]
	}
	return hits
}
