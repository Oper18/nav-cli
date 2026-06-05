package qdrant

import (
	"strings"
	"testing"
)

func TestBuildEmbedChunks_SmallFitsSingleChunk(t *testing.T) {
	p := Payload{
		Symbol:  "Foo",
		Type:    "function",
		Content: "func Foo() {\n\treturn\n}",
	}
	chunks := BuildEmbedChunks(p, 8192)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Content != normalizeContent(p.Content) {
		t.Errorf("chunk content = %q, want %q", chunks[0].Content, normalizeContent(p.Content))
	}
	if chunks[0].Text != BuildEmbedText(p) {
		t.Errorf("single-chunk text should equal BuildEmbedText")
	}
}

func TestBuildEmbedChunks_LargeSplitsAndStaysUnderBudget(t *testing.T) {
	// A code body far larger than the budget.
	body := strings.Repeat("x = compute(value)\n", 2000)
	p := Payload{
		Symbol:  "Big",
		Type:    "function",
		Content: body,
	}
	const budget = 4000
	chunks := BuildEmbedChunks(p, budget)
	if len(chunks) < 2 {
		t.Fatalf("expected the body to be split, got %d chunk(s)", len(chunks))
	}

	// Every chunk's embed text must respect the budget...
	var reassembled strings.Builder
	for i, ch := range chunks {
		if got := len([]rune(ch.Text)); got > budget {
			t.Errorf("chunk %d text length %d exceeds budget %d", i, got, budget)
		}
		reassembled.WriteString(ch.Content)
	}

	// ...and the chunk contents must reassemble the normalized code exactly.
	if reassembled.String() != normalizeContent(body) {
		t.Errorf("reassembled content does not match original normalized body")
	}
}

func TestBuildEmbedChunks_ZeroBudgetDisablesSplitting(t *testing.T) {
	p := Payload{Symbol: "Foo", Content: strings.Repeat("a", 100000)}
	chunks := BuildEmbedChunks(p, 0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with non-positive budget, got %d", len(chunks))
	}
}

func TestID_ChunkDisambiguates(t *testing.T) {
	a := ID("main", "Foo", 0)
	b := ID("main", "Foo", 1)
	if a == b {
		t.Errorf("ID should differ by chunk number, both = %s", a)
	}
	if a != ID("main", "Foo", 0) {
		t.Errorf("ID should be deterministic for the same inputs")
	}
}
