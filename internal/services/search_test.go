package services

import (
	"testing"

	"nav/internal/db/qdrant"
)

func hit(branch, file, symbol string, score float32) qdrant.Hit {
	return qdrant.Hit{
		Score: score,
		Payload: qdrant.Payload{
			Branch:   branch,
			FilePath: file,
			Symbol:   symbol,
		},
	}
}

// TestCollapseChunksPrefersCurrentBranchOverInherited guards the whole point
// of searching a branch chain: when the same file+symbol shows up tagged
// under both the current branch and an ancestor it was inherited from, the
// current branch's copy must win regardless of score, and it must not also
// appear a second time as an "inherited duplicate".
func TestCollapseChunksPrefersCurrentBranchOverInherited(t *testing.T) {
	hits := []qdrant.Hit{
		hit("main", "a.go", "Foo", 0.95), // inherited, scores higher
		hit("feature", "a.go", "Foo", 0.80),
		hit("feature", "b.go", "Bar", 0.90), // only ever embedded on feature
	}
	rank := branchChainRank([]string{"feature", "main"})

	out := collapseChunks(hits, rank)
	if len(out) != 2 {
		t.Fatalf("collapseChunks returned %d hits, want 2: %+v", len(out), out)
	}

	byKey := make(map[string]qdrant.Hit)
	for _, h := range out {
		byKey[h.Payload.FilePath+"/"+h.Payload.Symbol] = h
	}

	foo, ok := byKey["a.go/Foo"]
	if !ok {
		t.Fatal("expected a.go/Foo in results")
	}
	if foo.Payload.Branch != "feature" {
		t.Errorf("a.go/Foo resolved to branch %q, want %q (current branch must win over inherited)", foo.Payload.Branch, "feature")
	}

	if _, ok := byKey["b.go/Bar"]; !ok {
		t.Error("expected b.go/Bar (only embedded on feature) to still surface")
	}
}

// TestCollapseChunksNoRankFallsBackToScore guards the unfiltered/no-chain
// case: with no branch preference, the highest-scoring chunk wins, matching
// the pre-chain behavior for a plain "no branch filter" search.
func TestCollapseChunksNoRankFallsBackToScore(t *testing.T) {
	hits := []qdrant.Hit{
		hit("main", "a.go", "Foo", 0.70),
		hit("other", "a.go", "Foo", 0.95),
	}

	out := collapseChunks(hits, nil)
	if len(out) != 1 {
		t.Fatalf("collapseChunks returned %d hits, want 1: %+v", len(out), out)
	}
	if out[0].Payload.Branch != "other" {
		t.Errorf("resolved to branch %q, want %q (highest score should win with no chain)", out[0].Payload.Branch, "other")
	}
}

// TestCollapseChunksSortsByScoreDescending guards that replacing a
// lower-priority entry with a higher-priority one (which may have a lower
// score) doesn't leave the final result out of score order.
func TestCollapseChunksSortsByScoreDescending(t *testing.T) {
	hits := []qdrant.Hit{
		hit("main", "z.go", "Z", 0.99),
		hit("main", "a.go", "Foo", 0.60),    // inherited, high-ish score
		hit("feature", "a.go", "Foo", 0.50), // current branch, lower score, must still win the key...
	}
	rank := branchChainRank([]string{"feature", "main"})

	out := collapseChunks(hits, rank)
	for i := 1; i < len(out); i++ {
		if out[i].Score > out[i-1].Score {
			t.Fatalf("results not sorted by descending score: %+v", out)
		}
	}
}
