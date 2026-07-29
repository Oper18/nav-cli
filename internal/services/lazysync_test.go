package services

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nav/internal/db"
	"nav/internal/parser"
)

func TestDiffSymbols(t *testing.T) {
	sdb, err := db.Open(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	unchangedContent := "func Unchanged() {\n\treturn\n}"
	unchangedHash := contentHash(unchangedContent)
	if err := db.UpsertChunk(sdb, db.Chunk{
		ChunkID: "chunk-unchanged", File: "a.go", Symbol: "Unchanged",
		ContentHash: unchangedHash, EmbeddedHash: unchangedHash, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed unchanged chunk: %v", err)
	}
	if err := db.UpsertChunk(sdb, db.Chunk{
		ChunkID: "chunk-stale-source", File: "a.go", Symbol: "Edited",
		ContentHash: "old-hash", EmbeddedHash: "old-hash", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed edited chunk: %v", err)
	}
	if err := db.UpsertChunk(sdb, db.Chunk{
		ChunkID: "chunk-removed", File: "a.go", Symbol: "Removed",
		ContentHash: "gone-hash", EmbeddedHash: "gone-hash", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed removed chunk: %v", err)
	}
	if err := db.UpsertChunk(sdb, db.Chunk{
		ChunkID: "chunk-deleted-file", File: "b.go", Symbol: "InDeletedFile",
		ContentHash: "x", EmbeddedHash: "x", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed deleted-file chunk: %v", err)
	}

	fileSymbols := map[string][]parser.Symbol{
		"a.go": {
			mkSymbol("Unchanged", unchangedContent),
			mkSymbol("Edited", "func Edited() {\n\treturn 2\n}"),
			mkSymbol("New", "func New() {\n\treturn 3\n}"),
		},
	}

	diff, err := diffSymbols(sdb, fileSymbols, []string{"b.go"})
	if err != nil {
		t.Fatalf("diffSymbols: %v", err)
	}

	dirtyNames := map[string]bool{}
	for _, s := range diff.dirty {
		dirtyNames[s.Symbol] = true
	}
	if dirtyNames["Unchanged"] {
		t.Error("Unchanged symbol should not be dirty")
	}
	if !dirtyNames["Edited"] {
		t.Error("Edited symbol (content_hash changed) should be dirty")
	}
	if !dirtyNames["New"] {
		t.Error("New symbol (no manifest row) should be dirty")
	}

	staleIDs := map[string]bool{}
	for _, c := range diff.staleChunks {
		staleIDs[c.ChunkID] = true
	}
	if !staleIDs["chunk-removed"] {
		t.Error("Removed symbol's chunk should be in staleChunks")
	}
	if !staleIDs["chunk-deleted-file"] {
		t.Error("deleted file's chunk should be in staleChunks")
	}
	if staleIDs["chunk-unchanged"] || staleIDs["chunk-stale-source"] {
		t.Error("live symbols' chunks must not be marked stale")
	}
}

// TestAlreadySyncedFileIsNotFlaggedAgain guards the bug where a file synced
// outside the prompt-hook path (e.g. by the git pre-commit/post-merge hooks)
// would never be recorded in the manifest, so every subsequent lazy sync
// treated it as unsynced and re-embedded it forever. Once a file's manifest
// row and last-sync-head are in place — however they got there — a lazy sync
// against an unchanged tree must detect nothing to do.
func TestAlreadySyncedFileIsNotFlaggedAgain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	content := "func Foo() {\n\treturn\n}"
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n\n"+content+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "init")

	sdb, err := db.Open(repo, "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	// Simulate what a completed sync leaves behind, regardless of which entry
	// point (prompt hook or git hook) produced it: a manifest row whose
	// embedded hash matches the current content, plus last_sync_head/at
	// pointing at the commit that was synced.
	hash := contentHash(content)
	if err := db.UpsertChunk(sdb, db.Chunk{
		ChunkID: "chunk-foo", File: "a.go", Symbol: "Foo",
		ContentHash: hash, EmbeddedHash: hash, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed synced chunk: %v", err)
	}
	if err := sdb.SetMeta(metaLastSyncHead, HeadCommit(repo)); err != nil {
		t.Fatal(err)
	}

	changed, deleted, _, err := detectChangedFiles(sdb, repo, "main")
	if err != nil {
		t.Fatalf("detectChangedFiles: %v", err)
	}
	if len(changed) != 0 || len(deleted) != 0 {
		t.Fatalf("expected nothing to sync for an already-synced, unchanged tree; got changed=%v deleted=%v", changed, deleted)
	}
}

func mkSymbol(name, content string) parser.Symbol {
	var s parser.Symbol
	s.Symbol = name
	s.Content = content
	s.Type = "function"
	return s
}

func TestResolveLocalImportDirGo(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/proj\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal", "widget"), 0755); err != nil {
		t.Fatal(err)
	}

	dir, ok := resolveLocalImportDir(repo, "main.go", parser.LangGo, "example.com/proj/internal/widget")
	if !ok || dir != "internal/widget" {
		t.Errorf("resolveLocalImportDir = %q, %v; want \"internal/widget\", true", dir, ok)
	}

	_, ok = resolveLocalImportDir(repo, "main.go", parser.LangGo, "fmt")
	if ok {
		t.Error("stdlib import should not resolve locally")
	}

	_, ok = resolveLocalImportDir(repo, "main.go", parser.LangGo, "example.com/proj/does/not/exist")
	if ok {
		t.Error("non-existent local directory should not resolve")
	}
}

func TestResolveLocalImportDirTS(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "src", "utils"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "utils", "helpers.ts"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	dir, ok := resolveLocalImportDir(repo, "src/app.ts", parser.LangTypeScript, "./utils/helpers")
	if !ok || dir != "src/utils" {
		t.Errorf("resolveLocalImportDir = %q, %v; want \"src/utils\", true", dir, ok)
	}

	_, ok = resolveLocalImportDir(repo, "src/app.ts", parser.LangTypeScript, "react")
	if ok {
		t.Error("bare package specifier should not resolve locally")
	}
}

func TestGitStatusAndChangeDetection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "init")

	sdb, err := db.Open(repo, "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	// Bootstrap case: no last_sync_head recorded yet, so every tracked file
	// counts as "changed since last sync" — otherwise a brand new project
	// with a clean working tree would never get indexed at all.
	changed, deleted, _, err := detectChangedFiles(sdb, repo, "main")
	if err != nil {
		t.Fatalf("detectChangedFiles: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no deletions on a clean tree, got %v", deleted)
	}
	found := false
	for _, f := range changed {
		if f == "a.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a.go among changed files on first-ever sync (bootstrap), got %v", changed)
	}

	// Record the current HEAD as "already synced", then make a new commit —
	// the commit-range diff should pick it up even with a clean working tree.
	head := HeadCommit(repo)
	if err := sdb.SetMeta(metaLastSyncHead, head); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "b.go")
	run("commit", "-q", "-m", "add b")

	changed, deleted, _, err = detectChangedFiles(sdb, repo, "main")
	if err != nil {
		t.Fatalf("detectChangedFiles after commit: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("expected no deletions, got %v", deleted)
	}
	found = false
	for _, f := range changed {
		if f == "b.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected b.go among changed files, got %v", changed)
	}

	// An uncommitted working-tree edit is picked up too.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n\nvar X = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := sdb.SetMeta(metaLastSyncHead, HeadCommit(repo)); err != nil {
		t.Fatal(err)
	}
	changed, _, _, err = detectChangedFiles(sdb, repo, "main")
	if err != nil {
		t.Fatalf("detectChangedFiles after working-tree edit: %v", err)
	}
	found = false
	for _, f := range changed {
		if f == "a.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a.go among changed files after working-tree edit, got %v", changed)
	}
}

// setupBranchTopology builds the fork-point ambiguity from a real repo's
// `git log --graph --oneline --decorate --all --simplify-by-decoration`:
// "dev" branches off "main" and gets a commit of its own, "main" then merges
// "dev" back in via a merge commit (so main and dev share the very same
// merge-base with anything forked off dev), and "feature" branches off
// dev's tip with one commit of its own. nav is told it already indexed both
// "main" and "dev" (db.Exists), but not "feature" — mirroring a freshly
// checked-out branch nav has never synced.
func setupBranchTopology(t *testing.T) (repo string, run func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo = t.TempDir()
	run = func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	writeFile := func(name, content string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	writeFile("a.txt", "c0\n")
	run("add", "a.txt")
	run("commit", "-q", "-m", "c0")

	run("checkout", "-q", "-b", "dev")
	writeFile("b.txt", "c1\n")
	run("add", "b.txt")
	run("commit", "-q", "-m", "c1")

	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "Merged in dev", "dev")

	run("checkout", "-q", "dev")
	run("checkout", "-q", "-b", "feature")
	writeFile("c.txt", "c3\n")
	run("add", "c.txt")
	run("commit", "-q", "-m", "c3")

	for _, b := range []string{"main", "dev"} {
		sdb, err := db.Open(repo, b)
		if err != nil {
			t.Fatalf("db.Open(%s): %v", b, err)
		}
		sdb.Close()
	}

	return repo, run
}

// TestDetectParentBranchPrefersClosestSharedHistory guards the tie-break: when
// two candidate branches share the exact same merge-base commit (because one
// of them later merged the other), the candidate whose own tip *is* that
// commit — the more direct ancestor — must win over the one that has since
// diverged further via its own merge.
func TestDetectParentBranchPrefersClosestSharedHistory(t *testing.T) {
	repo, run := setupBranchTopology(t)

	parent, base, found := detectParentBranch(repo, "feature")
	if !found {
		t.Fatal("expected a parent branch to be found")
	}
	if parent != "dev" {
		t.Errorf("parent = %q, want %q (main shares the same merge-base only via its later merge commit)", parent, "dev")
	}
	if want := run("rev-parse", "dev"); base != want {
		t.Errorf("mergeBase = %q, want dev's tip %q", base, want)
	}
}

// TestBootstrapDiffsOnlyAgainstParentBranch guards the actual payoff: a
// brand-new branch's first-ever sync must only flag the files that differ
// from its detected parent, not every tracked file in the repo — otherwise
// checking out a new branch would re-embed the whole project from scratch.
func TestBootstrapDiffsOnlyAgainstParentBranch(t *testing.T) {
	repo, _ := setupBranchTopology(t)

	sdb, err := db.Open(repo, "feature")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	changed, deleted, parentBranch, err := detectChangedFiles(sdb, repo, "feature")
	if err != nil {
		t.Fatalf("detectChangedFiles: %v", err)
	}
	if parentBranch != "dev" {
		t.Fatalf("parentBranch = %q, want %q", parentBranch, "dev")
	}
	if len(deleted) != 0 {
		t.Errorf("expected no deletions, got %v", deleted)
	}
	if len(changed) != 1 || changed[0] != "c.txt" {
		t.Errorf("changed = %v, want only [c.txt] — a.txt/b.txt are unchanged from parent branch dev and must not be re-embedded", changed)
	}
}

// TestSyncFilesPersistsParentBranchMeta guards that syncFiles writes the
// detected parent branch into the manifest even when the bootstrap diff
// found nothing to embed (a brand new branch with no commits yet beyond its
// fork point) — otherwise the parent link, and the fast no-op path, would
// never be recorded and every future sync would redo bootstrap detection.
func TestSyncFilesPersistsParentBranchMeta(t *testing.T) {
	repo, _ := setupBranchTopology(t)

	sdb, err := db.Open(repo, "feature")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	if _, err := syncFiles(context.Background(), sdb, "testproj", repo, "feature", nil, nil, "dev", false); err != nil {
		t.Fatalf("syncFiles: %v", err)
	}

	got, ok, err := sdb.GetMeta(metaParentBranch)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if !ok || got != "dev" {
		t.Errorf("parent_branch meta = %q, ok=%v; want %q, true", got, ok, "dev")
	}
}

// TestUpdateFileGraphOnlyTouchesDirtySymbols guards the actual point of the
// symbol-scoped graph rewrite: syncing a file where only one of several
// symbols actually changed must leave every other symbol's node and edges
// completely alone — not tear them down and recreate them identically. It
// plants a marker edge on the untouched symbol's node that only
// DeleteOutgoingEdges (the full-rebuild path's teardown call) would remove,
// and asserts it survives.
func TestUpdateFileGraphOnlyTouchesDirtySymbols(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sdb, err := db.Open(repo, "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	fooID := parser.SymbolNodeID("a.go", "Foo")
	barID := parser.SymbolNodeID("a.go", "Bar")
	markerID := "sym:marker"

	if err := sdb.WithTx(func(tx *sql.Tx) error {
		if err := db.UpsertNode(tx, db.Node{ID: fooID, Kind: db.KindFunc, Name: "Foo", File: "a.go", Line: 1, Summary: "old foo"}); err != nil {
			return err
		}
		if err := db.UpsertNode(tx, db.Node{ID: barID, Kind: db.KindFunc, Name: "Bar", File: "a.go", Line: 5, Summary: "bar summary"}); err != nil {
			return err
		}
		if err := db.UpsertNode(tx, db.Node{ID: markerID, Kind: db.KindFunc, Name: "Marker"}); err != nil {
			return err
		}
		// A synthetic outgoing edge on Bar's node: only a call to
		// DeleteOutgoingEdges(barID) — i.e. treating Bar as dirty or torn
		// down — would remove this.
		return db.InsertEdge(tx, db.Edge{Src: barID, Dst: markerID, Kind: db.EdgeCalls})
	}); err != nil {
		t.Fatalf("seeding graph: %v", err)
	}

	allSymbols := []parser.Symbol{
		mkSymbol("Foo", "func Foo() {\n\treturn 2\n}"), // dirty: content changed
		mkSymbol("Bar", "func Bar() {\n\treturn\n}"),   // unchanged
	}
	dirty := []parser.Symbol{allSymbols[0]}

	if err := sdb.WithTx(func(tx *sql.Tx) error {
		return updateFileGraph(tx, repo, "a.go", allSymbols, dirty, nil)
	}); err != nil {
		t.Fatalf("updateFileGraph: %v", err)
	}

	bar, ok, err := db.NodeByID(sdb, barID)
	if err != nil {
		t.Fatalf("NodeByID(Bar): %v", err)
	}
	if !ok {
		t.Fatal("Bar's node should still exist")
	}
	if bar.Summary != "bar summary" {
		t.Errorf("Bar's summary = %q, want unchanged %q — it was not dirty and should not have been touched", bar.Summary, "bar summary")
	}

	edges, err := db.EdgesFrom(sdb, barID)
	if err != nil {
		t.Fatalf("EdgesFrom(Bar): %v", err)
	}
	foundMarker := false
	for _, e := range edges {
		if e.Dst == markerID {
			foundMarker = true
		}
	}
	if !foundMarker {
		t.Error("Bar's marker edge was removed — an unchanged symbol's edges must not be touched")
	}

	foo, ok, err := db.NodeByID(sdb, fooID)
	if err != nil {
		t.Fatalf("NodeByID(Foo): %v", err)
	}
	if !ok || foo.Summary == "old foo" {
		t.Errorf("Foo's node should have been refreshed (it was dirty); summary = %q", foo.Summary)
	}
}
