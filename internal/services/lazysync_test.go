package services

import (
	"os"
	"os/exec"
	"path/filepath"
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

	changed, deleted, err := detectChangedFiles(sdb, repo)
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
	changed, deleted, err := detectChangedFiles(sdb, repo)
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

	changed, deleted, err = detectChangedFiles(sdb, repo)
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
	changed, _, err = detectChangedFiles(sdb, repo)
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
