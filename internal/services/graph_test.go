package services

import (
	"strings"
	"testing"

	"nav/internal/db"
)

func seedTinyGraph(t *testing.T, sdb *db.DB) {
	t.Helper()
	nodes := []db.Node{
		{ID: "pkg:internal/api", Kind: db.KindPackage, Name: "internal/api"},
		{ID: "file:internal/api/handler.go", Kind: db.KindFile, Name: "handler.go", File: "internal/api/handler.go"},
		{ID: "sym:internal/api/handler.go#Handle", Kind: db.KindFunc, Name: "Handle", File: "internal/api/handler.go", Line: 10, Summary: "Handles inbound requests."},
		{ID: "sym:internal/api/handler.go#helper", Kind: db.KindFunc, Name: "helper", File: "internal/api/handler.go", Line: 20, Summary: "Shared helper logic."},
		{ID: "sym:cmd/main.go#main", Kind: db.KindFunc, Name: "main", File: "cmd/main.go", Line: 5},
		{ID: "pkg:cmd", Kind: db.KindPackage, Name: "cmd"},
		{ID: "file:cmd/main.go", Kind: db.KindFile, Name: "main.go", File: "cmd/main.go"},
	}
	for _, n := range nodes {
		if err := db.UpsertNode(sdb, n); err != nil {
			t.Fatalf("UpsertNode(%s): %v", n.ID, err)
		}
	}
	edges := []db.Edge{
		{Src: "sym:internal/api/handler.go#Handle", Dst: "sym:internal/api/handler.go#helper", Kind: db.EdgeCalls},
		{Src: "sym:cmd/main.go#main", Dst: "sym:internal/api/handler.go#Handle", Kind: db.EdgeCalls},
		{Src: "file:cmd/main.go", Dst: "pkg:internal/api", Kind: db.EdgeImports},
	}
	for _, e := range edges {
		if err := db.InsertEdge(sdb, e); err != nil {
			t.Fatalf("InsertEdge(%+v): %v", e, err)
		}
	}
}

func TestRenderGraphSummary(t *testing.T) {
	sdb, err := db.Open(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	digest, err := renderGraphSummary(sdb)
	if err != nil {
		t.Fatalf("renderGraphSummary: %v", err)
	}
	for _, want := range []string{"internal/api", "cmd", "main", "helper (1 callers)"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest missing %q; got:\n%s", want, digest)
		}
	}
	if strings.Contains(digest, "pkg:ext:") {
		t.Error("digest should never mention raw external package node ids")
	}
}

func TestGraphSummaryDigestCaching(t *testing.T) {
	sdb, err := db.Open(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	first, err := graphSummaryDigest(sdb)
	if err != nil {
		t.Fatalf("graphSummaryDigest: %v", err)
	}

	// Without any graph change, a second call must hit the cache and return
	// the identical stored digest without touching the underlying data.
	second, err := graphSummaryDigest(sdb)
	if err != nil {
		t.Fatalf("graphSummaryDigest (cached): %v", err)
	}
	if first != second {
		t.Error("expected cached digest to be identical across calls")
	}

	// A graph mutation that's actually visible in the rendered digest (a new
	// package) bumps graph_version, so the digest must regenerate and pick
	// it up on the next call.
	if err := db.UpsertNode(sdb, db.Node{ID: "pkg:internal/newpkg", Kind: db.KindPackage, Name: "internal/newpkg"}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	third, err := graphSummaryDigest(sdb)
	if err != nil {
		t.Fatalf("graphSummaryDigest (after change): %v", err)
	}
	if third == first {
		t.Error("expected digest to change after a graph mutation")
	}
}

func TestRenderProjectStructure(t *testing.T) {
	sdb, err := db.Open(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	structure, err := renderProjectStructure(sdb)
	if err != nil {
		t.Fatalf("renderProjectStructure: %v", err)
	}

	for _, want := range []string{"internal/api/", "handler.go (2 symbols)", "cmd/", "main.go (1 symbols)"} {
		if !strings.Contains(structure, want) {
			t.Errorf("structure missing %q; got:\n%s", want, structure)
		}
	}
	if strings.Contains(structure, "pkg:ext:") {
		t.Error("structure should never mention raw external package node ids")
	}
}

func TestProjectStructureDigestCaching(t *testing.T) {
	sdb, err := db.Open(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	first, err := projectStructureDigest(sdb)
	if err != nil {
		t.Fatalf("projectStructureDigest: %v", err)
	}

	// Without any graph change, a second call must hit the cache and return
	// the identical stored digest without touching the underlying data.
	second, err := projectStructureDigest(sdb)
	if err != nil {
		t.Fatalf("projectStructureDigest (cached): %v", err)
	}
	if first != second {
		t.Error("expected cached digest to be identical across calls")
	}

	// A graph mutation that's actually visible in the rendered digest (a new
	// file in a new package) bumps graph_version, so the digest must
	// regenerate and pick it up on the next call.
	if err := db.UpsertNode(sdb, db.Node{ID: "pkg:internal/newpkg", Kind: db.KindPackage, Name: "internal/newpkg"}); err != nil {
		t.Fatalf("UpsertNode(pkg): %v", err)
	}
	if err := db.UpsertNode(sdb, db.Node{ID: "file:internal/newpkg/new.go", Kind: db.KindFile, Name: "new.go", File: "internal/newpkg/new.go"}); err != nil {
		t.Fatalf("UpsertNode(file): %v", err)
	}
	third, err := projectStructureDigest(sdb)
	if err != nil {
		t.Fatalf("projectStructureDigest (after change): %v", err)
	}
	if third == first {
		t.Error("expected digest to change after a graph mutation")
	}
	if !strings.Contains(third, "new.go") {
		t.Errorf("expected regenerated digest to include the new file; got:\n%s", third)
	}
}

func TestResolvePackageOrFile(t *testing.T) {
	sdb, err := db.Open(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	id, node, err := resolvePackageOrFile(sdb, "cmd/main.go")
	if err != nil {
		t.Fatalf("resolvePackageOrFile(file): %v", err)
	}
	if id != "file:cmd/main.go" || node.Kind != db.KindFile {
		t.Errorf("resolvePackageOrFile(file) = %q, %+v", id, node)
	}

	id, node, err = resolvePackageOrFile(sdb, "internal/api")
	if err != nil {
		t.Fatalf("resolvePackageOrFile(pkg): %v", err)
	}
	if id != "pkg:internal/api" || node.Kind != db.KindPackage {
		t.Errorf("resolvePackageOrFile(pkg) = %q, %+v", id, node)
	}

	id, _, err = resolvePackageOrFile(sdb, "does/not/exist")
	if err != nil {
		t.Fatalf("resolvePackageOrFile(missing): %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id for a missing target, got %q", id)
	}
}

func TestFilterSymbolNodes(t *testing.T) {
	in := []db.Node{
		{ID: "pkg:x", Kind: db.KindPackage},
		{ID: "file:x.go", Kind: db.KindFile},
		{ID: "sym:x.go#F", Kind: db.KindFunc},
		{ID: "sym:x.go#T", Kind: db.KindType},
	}
	out := filterSymbolNodes(in)
	if len(out) != 2 {
		t.Fatalf("filterSymbolNodes returned %d nodes, want 2: %+v", len(out), out)
	}
}
