package services

import (
	"testing"

	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/parser"
)

func TestManifestMatchesOnlyEqualEmbeddedHash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src, err := db.Open("p", "dev")
	if err != nil {
		t.Fatal(err)
	}
	same := contentHash("func Same() {}")
	for _, c := range []db.Chunk{
		{ChunkID: "c-same", File: "a.go", Symbol: "Same", ContentHash: same, EmbeddedHash: same},
		{ChunkID: "c-old", File: "a.go", Symbol: "Edited", ContentHash: "x", EmbeddedHash: "x"},
	} {
		if err := db.UpsertChunk(src, c); err != nil {
			t.Fatal(err)
		}
	}
	src.Close()

	diff := &symbolDiff{
		dirty:  []parser.Symbol{mkSymbol("Same", "func Same() {}"), mkSymbol("Edited", "func Edited() {}")},
		hashOf: map[string]string{},
	}
	for i := range diff.dirty {
		diff.dirty[i].FilePath = "a.go"
		diff.hashOf[symbolKey("a.go", diff.dirty[i].Symbol)] = contentHash(diff.dirty[i].Content)
	}

	got, err := manifestMatches("p", "dev", diff, map[int]bool{0: true, 1: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0]) != 1 || got[0][0].ChunkID != "c-same" {
		t.Errorf("matches = %v, want only symbol 0 -> c-same", got)
	}
}

func TestCollectChunksRequiresCompleteOrderedSet(t *testing.T) {
	rows := []db.Chunk{{ChunkID: "b"}, {ChunkID: "a"}}
	pt := func(n, count int) qdrant.Point {
		return qdrant.Point{Vector: []float32{1}, Payload: qdrant.Payload{ChunkNumber: n, ChunkCount: count}}
	}

	got, ok := collectChunks(rows, map[string]qdrant.Point{"a": pt(0, 2), "b": pt(1, 2)})
	if !ok || got[0].Payload.ChunkNumber != 0 || got[1].Payload.ChunkNumber != 1 {
		t.Errorf("complete set: ok=%v got=%v", ok, got)
	}
	if _, ok := collectChunks(rows, map[string]qdrant.Point{"a": pt(0, 2)}); ok {
		t.Error("missing point must not be reused")
	}
	if _, ok := collectChunks(rows, map[string]qdrant.Point{"a": pt(0, 3), "b": pt(1, 3)}); ok {
		t.Error("chunk_count mismatch must not be reused")
	}
}

func TestReuseEmbeddingsNoopWithoutQdrant(t *testing.T) {
	diff := &symbolDiff{dirty: []parser.Symbol{mkSymbol("A", "func A() {}")}}
	if pts, n := reuseEmbeddings(nil, nil, "c", "p", t.TempDir(), "main", diff); pts != nil || n != 0 {
		t.Errorf("got %v, %d; want nil, 0", pts, n)
	}
}
