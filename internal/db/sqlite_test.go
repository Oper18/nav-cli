package db

import (
	"os"
	"testing"
)

func TestOpenIsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.Name()

	db, err := Open(project, "main")
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db.SetMeta("k", "v"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := Open(project, "main")
	if err != nil {
		t.Fatalf("second Open (should be a no-op migration): %v", err)
	}
	defer db2.Close()

	v, ok, err := db2.GetMeta("k")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if !ok || v != "v" {
		t.Fatalf("GetMeta = %q, %v; want \"v\", true", v, ok)
	}
}

func TestOpenIsolatesBranches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.Name()

	main, err := Open(project, "main")
	if err != nil {
		t.Fatalf("Open(main): %v", err)
	}
	if err := main.SetMeta("k", "on-main"); err != nil {
		t.Fatalf("SetMeta(main): %v", err)
	}
	main.Close()

	feature, err := Open(project, "feature/foo")
	if err != nil {
		t.Fatalf("Open(feature/foo): %v", err)
	}
	defer feature.Close()

	if _, ok, err := feature.GetMeta("k"); err != nil {
		t.Fatalf("GetMeta(feature): %v", err)
	} else if ok {
		t.Fatal("expected feature/foo's db to start empty, independent of main's")
	}

	if DBPath(project, "main") == DBPath(project, "feature/foo") {
		t.Fatal("expected distinct branches to resolve to distinct db paths")
	}
}

func TestResetBranchRemovesAndAllowsCleanReopen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.Name()

	sdb, err := Open(project, "main")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sdb.SetMeta("k", "v"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	sdb.Close()

	if _, err := os.Stat(DBPath(project, "main")); err != nil {
		t.Fatalf("expected db file to exist before reset: %v", err)
	}

	if err := ResetBranch(project, "main"); err != nil {
		t.Fatalf("ResetBranch: %v", err)
	}

	if _, err := os.Stat(DBPath(project, "main")); !os.IsNotExist(err) {
		t.Fatalf("expected db file to be gone after reset, stat err = %v", err)
	}

	// ResetBranch on an already-clean branch must be a no-op, not an error.
	if err := ResetBranch(project, "main"); err != nil {
		t.Fatalf("ResetBranch (already clean): %v", err)
	}

	// Reopening after reset must recreate cleanly (fresh migrations, no
	// leftover meta from before).
	sdb2, err := Open(project, "main")
	if err != nil {
		t.Fatalf("Open (after reset): %v", err)
	}
	defer sdb2.Close()
	if _, ok, err := sdb2.GetMeta("k"); err != nil || ok {
		t.Fatalf("expected clean db after reset, got ok=%v err=%v", ok, err)
	}
}

func TestResetBranchLeavesOtherBranchesAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.Name()

	main, err := Open(project, "main")
	if err != nil {
		t.Fatalf("Open(main): %v", err)
	}
	if err := main.SetMeta("k", "v"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	main.Close()

	feature, err := Open(project, "feature")
	if err != nil {
		t.Fatalf("Open(feature): %v", err)
	}
	if err := feature.SetMeta("k", "v"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	feature.Close()

	if err := ResetBranch(project, "feature"); err != nil {
		t.Fatalf("ResetBranch(feature): %v", err)
	}

	if _, err := os.Stat(DBPath(project, "main")); err != nil {
		t.Fatalf("expected main's db to survive resetting feature: %v", err)
	}
	if _, err := os.Stat(DBPath(project, "feature")); !os.IsNotExist(err) {
		t.Fatalf("expected feature's db to be gone, stat err = %v", err)
	}
}

func TestResetAllRemovesEveryBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.Name()

	for _, branch := range []string{"main", "feature/foo", "release-1.0"} {
		sdb, err := Open(project, branch)
		if err != nil {
			t.Fatalf("Open(%s): %v", branch, err)
		}
		if err := sdb.SetMeta("k", "v"); err != nil {
			t.Fatalf("SetMeta(%s): %v", branch, err)
		}
		sdb.Close()
	}

	if err := ResetAll(project); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}

	for _, branch := range []string{"main", "feature/foo", "release-1.0"} {
		if _, err := os.Stat(DBPath(project, branch)); !os.IsNotExist(err) {
			t.Fatalf("expected %s's db to be gone after ResetAll, stat err = %v", branch, err)
		}
	}

	// ResetAll against a project that was never created must be a no-op, not
	// an error.
	if err := ResetAll("never-indexed-project"); err != nil {
		t.Fatalf("ResetAll (no project dir yet): %v", err)
	}
}

func TestChunkDirtyDetection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := Open(t.Name(), "main")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	c := Chunk{ChunkID: "abc", File: "f.go", Symbol: "Foo", ContentHash: "h1", EmbeddedHash: "h1", UpdatedAt: 1}
	if err := UpsertChunk(db.sql, c); err != nil {
		t.Fatalf("UpsertChunk: %v", err)
	}

	rows, err := ChunksForFile(db.sql, "f.go")
	if err != nil {
		t.Fatalf("ChunksForFile: %v", err)
	}
	if len(rows) != 1 || rows[0].ContentHash != rows[0].EmbeddedHash {
		t.Fatalf("expected one clean row, got %+v", rows)
	}

	// Simulate a source edit: content_hash moves ahead of embedded_hash.
	c.ContentHash = "h2"
	if err := UpsertChunk(db.sql, c); err != nil {
		t.Fatalf("UpsertChunk (dirty): %v", err)
	}
	rows, err = ChunksForFile(db.sql, "f.go")
	if err != nil {
		t.Fatalf("ChunksForFile: %v", err)
	}
	if len(rows) != 1 || rows[0].ContentHash == rows[0].EmbeddedHash {
		t.Fatalf("expected the row to be dirty (content_hash != embedded_hash), got %+v", rows)
	}

	if err := DeleteChunksForFile(db.sql, "f.go"); err != nil {
		t.Fatalf("DeleteChunksForFile: %v", err)
	}
	rows, err = ChunksForFile(db.sql, "f.go")
	if err != nil {
		t.Fatalf("ChunksForFile: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows after delete, got %+v", rows)
	}
}

func TestGraphWalk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := Open(t.Name(), "main")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// main -> a -> b -> c (calls), each a func node in the same package.
	for _, n := range []Node{
		{ID: "sym:main.go#main", Kind: KindFunc, Name: "main", File: "main.go", Line: 1},
		{ID: "sym:a.go#A", Kind: KindFunc, Name: "A", File: "a.go", Line: 1},
		{ID: "sym:b.go#B", Kind: KindFunc, Name: "B", File: "b.go", Line: 1},
		{ID: "sym:c.go#C", Kind: KindFunc, Name: "C", File: "c.go", Line: 1},
	} {
		if err := UpsertNode(db.sql, n); err != nil {
			t.Fatalf("UpsertNode(%s): %v", n.ID, err)
		}
	}
	edges := []Edge{
		{Src: "sym:main.go#main", Dst: "sym:a.go#A", Kind: EdgeCalls},
		{Src: "sym:a.go#A", Dst: "sym:b.go#B", Kind: EdgeCalls},
		{Src: "sym:b.go#B", Dst: "sym:c.go#C", Kind: EdgeCalls},
	}
	for _, e := range edges {
		if err := InsertEdge(db.sql, e); err != nil {
			t.Fatalf("InsertEdge(%+v): %v", e, err)
		}
	}

	// Callers of C, depth 1: only B.
	callers, err := Callers(db.sql, []string{"sym:c.go#C"}, 1)
	if err != nil {
		t.Fatalf("Callers depth 1: %v", err)
	}
	if len(callers) != 1 || callers[0].ID != "sym:b.go#B" {
		t.Fatalf("Callers depth 1 = %+v, want [B]", callers)
	}

	// Callers of C, depth 3: B, A, main.
	callers, err = Callers(db.sql, []string{"sym:c.go#C"}, 3)
	if err != nil {
		t.Fatalf("Callers depth 3: %v", err)
	}
	if len(callers) != 3 {
		t.Fatalf("Callers depth 3 = %+v, want 3 nodes", callers)
	}

	// Fan-in: B and C and A each called once.
	fanin, err := TopFanIn(db.sql, 10)
	if err != nil {
		t.Fatalf("TopFanIn: %v", err)
	}
	if len(fanin) != 3 {
		t.Fatalf("TopFanIn = %+v, want 3 entries", fanin)
	}

	// Deleting outgoing edges of A should stop the walk from main reaching B/C.
	if err := DeleteOutgoingEdges(db.sql, "sym:a.go#A"); err != nil {
		t.Fatalf("DeleteOutgoingEdges: %v", err)
	}
	callers, err = Callers(db.sql, []string{"sym:c.go#C"}, 3)
	if err != nil {
		t.Fatalf("Callers after delete: %v", err)
	}
	if len(callers) != 1 || callers[0].ID != "sym:b.go#B" {
		t.Fatalf("Callers after delete = %+v, want [B]", callers)
	}
}
