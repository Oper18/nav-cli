package services

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"

	"nav/config"
	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/llm"
	"nav/internal/parser"
)

// LazySyncResult summarises what a lazy sync run did.
type LazySyncResult struct {
	Skipped        bool // another sync was already running; this run did nothing
	DryRun         bool
	ChunksEmbedded int
	ChunksRemoved  int
}

// Summary formats the one-line result nav prints after a sync.
func (r LazySyncResult) Summary() string {
	if r.Skipped {
		return "sync skipped: another sync is already running"
	}
	suffix := ""
	if r.DryRun {
		suffix = " (dry run)"
	}
	return fmt.Sprintf("synced: %d chunks re-embedded, %d removed%s", r.ChunksEmbedded, r.ChunksRemoved, suffix)
}

// lazySyncLockWait bounds how long a hook invocation waits for a concurrent
// sync to finish before giving up and answering the prompt against whatever
// is already indexed, rather than blocking the user.
const lazySyncLockWait = 4 * time.Second

const (
	metaLastSyncHead = "last_sync_head"
	metaLastSyncAt   = "last_sync_at"
	// metaParentBranch records which already-indexed branch a branch's
	// embeddings were bootstrapped from (see detectParentBranch). It is
	// written once, on that branch's first-ever sync, and never overwritten
	// afterwards.
	metaParentBranch = "parent_branch"
)

// maxParentChainDepth bounds how far BranchChain walks up recorded
// parent_branch links, guarding against a pathological or cyclic chain.
const maxParentChainDepth = 16

// LazySync detects files changed since the last sync (via git status/HEAD
// movement, or mtime for non-git projects), re-embeds only the chunks whose
// content actually changed (tracked via the SQLite manifest in
// .nav/nav-<branch>.db), removes deleted ones, and keeps the knowledge graph
// there in lockstep. The manifest and graph are per-branch — what files and
// symbols exist can differ meaningfully between branches — so each branch
// gets its own database file. It is idempotent and safe to call on every
// prompt: with nothing dirty it does one git status call plus a couple of
// meta reads and returns.
func LazySync(ctx context.Context, project, repoPath string, dryRun bool) (LazySyncResult, error) {
	var result LazySyncResult
	branch := CurrentBranch(repoPath)

	ensureMigratedFromRepo(project, repoPath)

	err := db.WithLock(project, lazySyncLockWait, func() error {
		sdb, err := db.Open(project, branch)
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		defer sdb.Close()

		changed, deleted, parentBranch, err := detectChangedFiles(sdb, project, repoPath, branch)
		if err != nil {
			return fmt.Errorf("detecting changed files: %w", err)
		}
		if len(changed) == 0 && len(deleted) == 0 && parentBranch == "" {
			return nil // fast no-op path: nothing to do, meta is already current
		}

		res, err := syncFiles(ctx, sdb, project, repoPath, branch, changed, deleted, parentBranch, dryRun)
		if err != nil {
			return err
		}
		result = res
		return nil
	})
	if err == db.ErrLocked {
		return LazySyncResult{Skipped: true}, nil
	}
	if err != nil {
		return LazySyncResult{}, err
	}
	return result, nil
}

// detectChangedFiles returns the set of files changed/deleted since the last
// sync, plus the parent branch this branch was bootstrapped from (non-empty
// only the first time a branch's own database is synced, and only when one
// was found — see detectParentBranch). For git repositories this unions the
// working-tree diff (git status) with a commit-range diff when HEAD moved
// since the last sync (covering commits made outside nav's own git hooks).
// For non-git projects it falls back to an mtime walk against the
// manifest's last-known files.
func detectChangedFiles(sdb *db.DB, project, repoPath, branch string) (changed, deleted []string, parentBranch string, err error) {
	if _, statErr := os.Stat(filepath.Join(repoPath, ".git")); statErr != nil {
		c, d, mErr := detectChangedFilesMtime(sdb, repoPath)
		return c, d, "", mErr
	}

	statusChanged, statusDeleted, err := GitStatusFiles(repoPath)
	if err != nil {
		return nil, nil, "", err
	}

	changedSet := make(map[string]bool)
	deletedSet := make(map[string]bool)
	for _, f := range statusChanged {
		changedSet[f] = true
	}
	for _, f := range statusDeleted {
		deletedSet[f] = true
	}

	head := HeadCommit(repoPath)
	lastHead, ok, err := sdb.GetMeta(metaLastSyncHead)
	if err != nil {
		return nil, nil, "", err
	}
	switch {
	case !ok:
		// Bootstrap: nav sync has never run against this branch before, so
		// there is no baseline to diff against. If another branch nav has
		// already indexed shares recent history with this one, treat that
		// branch's tip as the baseline instead of the repo's creation — only
		// what actually differs from it needs (re-)embedding, and everything
		// else is served from that branch's existing points at search time
		// (see BranchChain). Absent any such candidate, fall back to
		// treating every tracked file as "changed since last sync", so a
		// project with no history to inherit from still gets fully indexed
		// without requiring a separate `nav index` first.
		if parent, mergeBase, found := detectParentBranch(project, repoPath, branch); found {
			raw, diffErr := RunGitCmd(repoPath, "diff", "--name-status", mergeBase, "HEAD")
			if diffErr != nil {
				fmt.Fprintf(os.Stderr, "nav: warn: diffing parent branch %s (%s..HEAD): %v\n", parent, mergeBase, diffErr)
			} else {
				c, d := ParseNameStatus(raw)
				for _, f := range c {
					changedSet[f] = true
				}
				for _, f := range d {
					deletedSet[f] = true
				}
				parentBranch = parent
			}
		}
		if parentBranch == "" {
			if lsOut, lsErr := RunGitCmd(repoPath, "ls-files"); lsErr == nil {
				for _, f := range SplitLines(lsOut) {
					changedSet[f] = true
				}
			}
		}
	case head != "" && lastHead != head:
		raw, diffErr := RunGitCmd(repoPath, "diff", "--name-status", lastHead, head)
		if diffErr != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: diffing %s..%s: %v\n", lastHead, head, diffErr)
		} else {
			c, d := ParseNameStatus(raw)
			for _, f := range c {
				changedSet[f] = true
			}
			for _, f := range d {
				deletedSet[f] = true
			}
		}
	}

	// A file that shows up as both changed and deleted (rename churn, or a
	// file staged then removed) is treated as deleted.
	for f := range deletedSet {
		delete(changedSet, f)
	}

	return setToSlice(changedSet), setToSlice(deletedSet), parentBranch, nil
}

// detectParentBranch finds the already-indexed local branch that branch most
// likely forked from, so a brand-new branch's first sync can diff against
// that fork point instead of re-embedding the whole tree. Candidates are
// restricted to branches with their own nav database (db.Exists) — there's
// nothing to inherit from a branch nav has never synced.
//
// Ranking: for each candidate, compute its merge-base with branch. The
// candidate whose merge-base commit is most recent wins (the closest shared
// history); ties (e.g. two candidates sharing the very same merge-base
// commit, which happens when one candidate later merged the other) are
// broken by preferring whichever candidate's own tip is fewest commits past
// that merge-base — i.e. the more direct ancestor. A final lexical
// tie-break keeps the choice deterministic.
func detectParentBranch(project, repoPath, branch string) (parent, mergeBase string, found bool) {
	branches, err := LocalBranches(repoPath)
	if err != nil {
		return "", "", false
	}

	var bestTime int64 = -1
	var bestDist int = -1
	for _, candidate := range branches {
		if candidate == "" || candidate == branch {
			continue
		}
		if !db.Exists(project, candidate) {
			continue
		}
		base, ok := MergeBase(repoPath, candidate, branch)
		if !ok {
			continue
		}
		ts, ok := CommitTimestamp(repoPath, base)
		if !ok {
			continue
		}
		dist, ok := CommitsAhead(repoPath, base, candidate)
		if !ok {
			continue
		}

		better := !found ||
			ts > bestTime ||
			(ts == bestTime && dist < bestDist) ||
			(ts == bestTime && dist == bestDist && candidate < parent)
		if better {
			found = true
			bestTime = ts
			bestDist = dist
			parent = candidate
			mergeBase = base
		}
	}
	return parent, mergeBase, found
}

// detectChangedFilesMtime is the non-git fallback: it walks the tree using
// the same filters nav index applies, and treats a file as changed when its
// mtime is newer than the last sync. Files present in the manifest but no
// longer found on disk are reported as deleted.
func detectChangedFilesMtime(sdb *db.DB, repoPath string) (changed, deleted []string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	lastSyncAtStr, ok, err := sdb.GetMeta(metaLastSyncAt)
	if err != nil {
		return nil, nil, err
	}
	var lastSyncAt int64
	if ok {
		lastSyncAt, _ = strconv.ParseInt(lastSyncAtStr, 10, 64)
	}

	seen := make(map[string]bool)
	walkErr := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if rel != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if parser.ShouldSkip(rel, cfg.Indexing.SkipPatterns) || parser.DetectLanguage(rel) == "" {
			return nil
		}
		seen[rel] = true
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if lastSyncAt == 0 || info.ModTime().Unix() > lastSyncAt {
			changed = append(changed, rel)
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("walking repository: %w", walkErr)
	}

	manifestFiles, err := db.AllFiles(sdb)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range manifestFiles {
		if !seen[f] {
			deleted = append(deleted, f)
		}
	}

	return changed, deleted, nil
}

func setToSlice(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// contentHash hashes the normalized form of a symbol's raw parsed content —
// deliberately not the LLM-generated summary, which is non-deterministic
// across runs. This means re-running sync with no source change never
// re-triggers embedding, even though summaries vary run to run.
func contentHash(rawContent string) string {
	return strconv.FormatUint(xxhash.Sum64String(qdrant.NormalizeContent(rawContent)), 16)
}

// symbolDiff is the result of comparing a fresh parse against the manifest.
type symbolDiff struct {
	dirty []parser.Symbol
	// hashOf maps "file\x00symbol" -> freshly computed content hash, for
	// every symbol currently present in a changed file (not just dirty
	// ones), so manifest rows can be written once embedding finishes.
	hashOf map[string]string
	// oldChunkIDs maps "file\x00symbol" -> the chunk_ids the manifest held
	// for a dirty symbol before this sync, so a shrinking/growing chunk
	// count can be reconciled after re-embedding produces the new set.
	oldChunkIDs map[string][]string
	// staleChunks are manifest rows for symbols that no longer exist at all
	// (removed from their file, or the file itself was deleted) — deleted
	// outright, never replaced.
	staleChunks []db.Chunk
}

func symbolKey(file, symbol string) string { return file + "\x00" + symbol }

// diffSymbols compares the freshly extracted symbols for each changed file
// (and the manifest state of every deleted file) against the manifest to
// find what actually needs re-embedding or removal.
func diffSymbols(sdb *db.DB, fileSymbols map[string][]parser.Symbol, deletedFiles []string) (symbolDiff, error) {
	diff := symbolDiff{
		hashOf:      make(map[string]string),
		oldChunkIDs: make(map[string][]string),
	}

	for rel, syms := range fileSymbols {
		existing, err := db.ChunksForFile(sdb, rel)
		if err != nil {
			return diff, err
		}
		bySymbol := make(map[string][]db.Chunk)
		for _, c := range existing {
			bySymbol[c.Symbol] = append(bySymbol[c.Symbol], c)
		}

		seen := make(map[string]bool, len(syms))
		for _, sym := range syms {
			seen[sym.Symbol] = true
			hash := contentHash(sym.Content)
			diff.hashOf[symbolKey(rel, sym.Symbol)] = hash

			rows := bySymbol[sym.Symbol]
			isDirty := len(rows) == 0
			for _, r := range rows {
				if r.EmbeddedHash != hash {
					isDirty = true
				}
			}
			if isDirty {
				diff.dirty = append(diff.dirty, sym)
				ids := make([]string, len(rows))
				for i, r := range rows {
					ids[i] = r.ChunkID
				}
				diff.oldChunkIDs[symbolKey(rel, sym.Symbol)] = ids
			}
		}
		for symbol, rows := range bySymbol {
			if !seen[symbol] {
				diff.staleChunks = append(diff.staleChunks, rows...)
			}
		}
	}

	for _, rel := range deletedFiles {
		rows, err := db.ChunksForFile(sdb, rel)
		if err != nil {
			return diff, err
		}
		diff.staleChunks = append(diff.staleChunks, rows...)
	}

	return diff, nil
}

// syncFiles re-parses changed files, diffs them against the manifest,
// re-embeds and re-upserts only what's dirty, removes what's gone, and keeps
// the knowledge graph in lockstep — all in the same run.
func syncFiles(ctx context.Context, sdb *db.DB, project, repoPath, branch string, changedFiles, deletedFiles []string, parentBranch string, dryRun bool) (LazySyncResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return LazySyncResult{}, fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return LazySyncResult{}, fmt.Errorf("loading credentials: %w", err)
	}

	collection := "nav_" + project

	var toProcess []string
	for _, rel := range changedFiles {
		if parser.ShouldSkip(rel, cfg.Indexing.SkipPatterns) || parser.DetectLanguage(rel) == "" {
			continue
		}
		toProcess = append(toProcess, rel)
	}

	fileSymbols := make(map[string][]parser.Symbol, len(toProcess))
	for _, rel := range toProcess {
		syms, extractErr := parser.ExtractSymbols(ctx, repoPath, rel, branch)
		if extractErr != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: extract %s: %v\n", rel, extractErr)
			continue
		}
		fileSymbols[rel] = syms
	}

	diff, err := diffSymbols(sdb, fileSymbols, deletedFiles)
	if err != nil {
		return LazySyncResult{}, err
	}

	if dryRun {
		return LazySyncResult{DryRun: true, ChunksEmbedded: len(diff.dirty), ChunksRemoved: len(diff.staleChunks)}, nil
	}

	var points []qdrant.Point
	var qdrantClient *db.Client

	if len(diff.dirty) > 0 || len(diff.staleChunks) > 0 || hasAnyOldChunks(diff) {
		if err := EnsureLocalQdrant(cfg); err != nil {
			return LazySyncResult{}, fmt.Errorf("ensuring local qdrant: %w", err)
		}
		qdrantClient, err = db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
		if err != nil {
			return LazySyncResult{}, fmt.Errorf("creating qdrant client: %w", err)
		}
		defer qdrantClient.Close()
		if err := qdrantClient.EnsureCollection(ctx, collection, cfg.Embedding.Dimension); err != nil {
			return LazySyncResult{}, fmt.Errorf("ensuring collection: %w", err)
		}
	}

	if len(diff.dirty) > 0 {
		llmClient := llm.NewClientWithEmbedTimeout(creds.OpenRouterAPIKey, cfg.LLM.Model, cfg.LLM.FallbackModels,
			time.Duration(cfg.LLM.RequestTimeout)*time.Second,
			time.Duration(cfg.Embedding.RequestTimeout)*time.Second,
			time.Duration(cfg.LLM.ReadmeTimeout)*time.Second)

		readme, readmeErr := config.ReadProjectReadme(project)
		if readmeErr != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: reading project readme: %v\n", readmeErr)
		}
		readmeContext := capRunes(readme, readmeContextCap)

		points, err = embedAndUpsertSymbols(ctx, cfg, llmClient, qdrantClient, collection, readmeContext, cfg.Indexing.Concurrency, diff.dirty)
		if err != nil {
			return LazySyncResult{}, err
		}
	}

	// Reconcile chunk counts: a re-embedded symbol may now span a different
	// number of chunks than before, so any of its old chunk ids missing from
	// the freshly produced set are orphaned and must be removed too.
	newIDsBySymbol := make(map[string]map[string]bool)
	for _, p := range points {
		key := symbolKey(p.Payload.FilePath, p.Payload.Symbol)
		if newIDsBySymbol[key] == nil {
			newIDsBySymbol[key] = make(map[string]bool)
		}
		newIDsBySymbol[key][p.ID] = true
	}
	var deleteIDs []string
	for key, oldIDs := range diff.oldChunkIDs {
		newSet := newIDsBySymbol[key]
		for _, old := range oldIDs {
			if !newSet[old] {
				deleteIDs = append(deleteIDs, old)
			}
		}
	}
	for _, row := range diff.staleChunks {
		deleteIDs = append(deleteIDs, row.ChunkID)
	}

	if len(deleteIDs) > 0 && qdrantClient != nil {
		if err := qdrantClient.Delete(ctx, collection, deleteIDs); err != nil {
			return LazySyncResult{}, fmt.Errorf("deleting stale points: %w", err)
		}
	}

	// Every SQLite write — manifest and graph — commits atomically, so the
	// index and the knowledge graph can never drift apart from each other.
	err = sdb.WithTx(func(tx *sql.Tx) error {
		now := time.Now().Unix()

		for _, p := range points {
			key := symbolKey(p.Payload.FilePath, p.Payload.Symbol)
			if err := db.UpsertChunk(tx, db.Chunk{
				ChunkID:      p.ID,
				File:         p.Payload.FilePath,
				Symbol:       p.Payload.Symbol,
				ContentHash:  diff.hashOf[key],
				EmbeddedHash: diff.hashOf[key],
				UpdatedAt:    now,
			}); err != nil {
				return err
			}
		}
		for _, id := range deleteIDs {
			if err := db.DeleteChunk(tx, id); err != nil {
				return err
			}
		}

		dirtyByFile := make(map[string][]parser.Symbol, len(fileSymbols))
		for _, s := range diff.dirty {
			dirtyByFile[s.FilePath] = append(dirtyByFile[s.FilePath], s)
		}
		removedByFile := make(map[string][]string, len(fileSymbols))
		for _, c := range diff.staleChunks {
			removedByFile[c.File] = append(removedByFile[c.File], c.Symbol)
		}
		for rel, syms := range fileSymbols {
			if err := updateFileGraph(tx, repoPath, rel, syms, dirtyByFile[rel], removedByFile[rel]); err != nil {
				return fmt.Errorf("updating graph for %s: %w", rel, err)
			}
		}
		for _, rel := range deletedFiles {
			if err := teardownFileGraph(tx, rel); err != nil {
				return fmt.Errorf("tearing down graph for %s: %w", rel, err)
			}
		}

		if head := HeadCommit(repoPath); head != "" {
			if err := db.SetMeta(tx, metaLastSyncHead, head); err != nil {
				return err
			}
		}
		if parentBranch != "" {
			if err := db.SetMeta(tx, metaParentBranch, parentBranch); err != nil {
				return err
			}
		}
		return db.SetMeta(tx, metaLastSyncAt, strconv.FormatInt(now, 10))
	})
	if err != nil {
		return LazySyncResult{}, fmt.Errorf("committing sync: %w", err)
	}

	return LazySyncResult{ChunksEmbedded: len(points), ChunksRemoved: len(deleteIDs)}, nil
}

func hasAnyOldChunks(diff symbolDiff) bool {
	return len(diff.oldChunkIDs) > 0
}

// updateFileGraph incrementally updates rel's persisted graph state: only
// dirty symbols (whose content hash actually changed — the same split
// diffSymbols already computed for the embedding manifest, so "what changed"
// is derived exactly once per sync) get their node and outgoing edges
// (defines, Go embeds/implements, calls) recomputed; removed symbols are
// torn down; every unchanged symbol in the file — node, summary, and edges —
// is left completely untouched. allSymbols (the file's full current parse)
// is only used to give the package/file nodes their bootstrap, to give the
// Go embeds/implements heuristics (which can look at a dirty symbol's
// unchanged siblings) their full context, and to re-resolve the file's
// "imports" edges, which are file-level rather than per-symbol and cheap
// enough (proportional to import count, not symbol count) to just redo on
// every sync of the file.
//
// One known imprecision, inherited from the existing best-effort graph (see
// resolveSymbolCalls and goImplementsEdges): an "implements" edge depends on
// a struct's method set, which lives on separate method symbols — so if a
// method is renamed/added without the struct symbol itself changing, that
// edge won't be recomputed until the struct (or that method) is next dirty.
func updateFileGraph(tx *sql.Tx, repoPath, rel string, allSymbols, dirty []parser.Symbol, removed []string) error {
	for _, name := range removed {
		id := parser.SymbolNodeID(rel, name)
		if err := db.DeleteOutgoingEdges(tx, id); err != nil {
			return err
		}
		if err := db.DeleteNode(tx, id); err != nil {
			return err
		}
	}

	dir := parser.PackageDir(rel)
	pkgID := parser.PackageNodeID(dir)
	fileID := parser.FileNodeID(rel)
	if err := db.UpsertNode(tx, db.Node{ID: pkgID, Kind: db.KindPackage, Name: dir}); err != nil {
		return err
	}
	if err := db.UpsertNode(tx, db.Node{ID: fileID, Kind: db.KindFile, Name: filepath.Base(rel), File: rel}); err != nil {
		return err
	}
	if err := db.InsertEdge(tx, db.Edge{Src: pkgID, Dst: fileID, Kind: db.EdgeDefines}); err != nil {
		return err
	}

	aliasToPkg, err := resolveFileImports(tx, repoPath, rel)
	if err != nil {
		return err
	}

	if len(dirty) == 0 {
		return nil
	}

	for _, sym := range dirty {
		if err := db.DeleteOutgoingEdges(tx, parser.SymbolNodeID(rel, sym.Symbol)); err != nil {
			return err
		}
	}
	for _, n := range parser.DirtySymbolNodes(rel, dirty) {
		if err := db.UpsertNode(tx, db.Node{ID: n.ID, Kind: n.Kind, Name: n.Name, File: n.File, Line: n.Line, Summary: n.Summary}); err != nil {
			return err
		}
	}
	for _, e := range parser.DirtySymbolEdges(rel, allSymbols, dirty) {
		if err := db.InsertEdge(tx, db.Edge{Src: e.Src, Dst: e.Dst, Kind: e.Kind}); err != nil {
			return err
		}
	}

	return resolveSymbolCalls(tx, rel, dir, aliasToPkg, dirty)
}

// teardownFileGraph removes every node (file + its symbols) defined in a
// deleted file, along with their outgoing edges. Package nodes are left in
// place — other files in the same package may still reference them, and an
// empty package node is harmless.
func teardownFileGraph(tx *sql.Tx, rel string) error {
	old, err := db.NodesInFile(tx, rel)
	if err != nil {
		return err
	}
	for _, n := range old {
		if err := db.DeleteOutgoingEdges(tx, n.ID); err != nil {
			return err
		}
		if err := db.DeleteNode(tx, n.ID); err != nil {
			return err
		}
	}
	return nil
}

// resolveFileImports re-resolves rel's import statements against the
// persistent graph and returns the alias -> package-node-id mapping
// resolveSymbolCalls needs for qualified (alias.Symbol) calls. It always
// clears and rebuilds rel's "imports" edges (DeleteOutgoingEdgesByKind), not
// just the "defines" edges the caller manages separately — there's no cheap
// signal for "did the import list change" short of re-parsing it, but this
// is proportional to import count, not symbol count, so redoing it on every
// sync of the file is cheap regardless of how many symbols in it are dirty.
func resolveFileImports(tx *sql.Tx, repoPath, rel string) (map[string]string, error) {
	lang := parser.DetectLanguage(rel)
	source, err := os.ReadFile(filepath.Join(repoPath, rel))
	if err != nil {
		return nil, nil // file vanished between listing and processing — nothing to resolve
	}

	fileID := parser.FileNodeID(rel)
	if err := db.DeleteOutgoingEdgesByKind(tx, fileID, db.EdgeImports); err != nil {
		return nil, err
	}

	aliasToPkg := make(map[string]string)
	for _, imp := range parser.ExtractFileImports(lang, source) {
		var dstID string
		if localDir, ok := resolveLocalImportDir(repoPath, rel, lang, imp.Path); ok {
			dstID = parser.PackageNodeID(localDir)
			if err := db.UpsertNode(tx, db.Node{ID: dstID, Kind: db.KindPackage, Name: localDir}); err != nil {
				return nil, err
			}
		} else {
			dstID = parser.ExternalPackageNodeID(imp.Path)
			if err := db.UpsertNode(tx, db.Node{ID: dstID, Kind: db.KindPackage, Name: imp.Path}); err != nil {
				return nil, err
			}
		}
		if err := db.InsertEdge(tx, db.Edge{Src: fileID, Dst: dstID, Kind: db.EdgeImports}); err != nil {
			return nil, err
		}
		aliasToPkg[imp.Alias] = dstID
	}

	return aliasToPkg, nil
}

// resolveSymbolCalls resolves symbols' calls against the persistent graph:
// bare calls resolve within the caller's own package, qualified calls
// (alias.Symbol) resolve by mapping the alias to an import, then to a
// same-package symbol. Anything it can't confidently resolve is left alone
// rather than guessed, per the spec. It's only ever called with a file's
// dirty symbols — an unchanged symbol's own calls list, extracted from its
// unchanged content, is still accurate, so its previously-resolved edges
// need no rework.
func resolveSymbolCalls(tx *sql.Tx, rel, dir string, aliasToPkg map[string]string, symbols []parser.Symbol) error {
	for _, sym := range symbols {
		callerID := parser.SymbolNodeID(rel, sym.Symbol)
		for _, call := range sym.Calls {
			var wantDir string
			var name string
			if idx := strings.Index(call, "."); idx >= 0 {
				alias := call[:idx]
				pkgID, ok := aliasToPkg[alias]
				if !ok || !isLocalPkgID(pkgID) {
					continue // unqualified alias, or a package we can't see inside — leave unresolved
				}
				wantDir = strings.TrimPrefix(pkgID, "pkg:")
				name = call[idx+1:]
			} else {
				wantDir = dir
				name = call
			}

			candidates, err := db.NodesByName(tx, name)
			if err != nil {
				return err
			}
			for _, c := range candidates {
				if c.ID == callerID {
					continue // ignore self-recursion, matching computeCalledBy's convention
				}
				if (c.Kind != db.KindFunc && c.Kind != db.KindMethod) || parser.PackageDir(c.File) != wantDir {
					continue
				}
				if err := db.InsertEdge(tx, db.Edge{Src: callerID, Dst: c.ID, Kind: db.EdgeCalls}); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func isLocalPkgID(id string) bool {
	return strings.HasPrefix(id, "pkg:") && !strings.HasPrefix(id, "pkg:ext:")
}

// resolveLocalImportDir best-effort resolves an import path to a directory
// inside the repo. It never guesses past a filesystem check: if the
// candidate directory doesn't exist, the import is treated as external.
func resolveLocalImportDir(repoPath, fromRel, lang, importPath string) (string, bool) {
	var candidate string
	switch lang {
	case parser.LangGo:
		mod := goModuleName(repoPath)
		if mod == "" {
			return "", false
		}
		switch {
		case importPath == mod:
			return ".", true
		case strings.HasPrefix(importPath, mod+"/"):
			candidate = strings.TrimPrefix(importPath, mod+"/")
		default:
			return "", false
		}
	case parser.LangTypeScript, parser.LangJavaScript:
		if !strings.HasPrefix(importPath, ".") {
			return "", false
		}
		candidate = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(fromRel), importPath)))
	case parser.LangPython:
		candidate = strings.ReplaceAll(importPath, ".", "/")
	default:
		return "", false
	}

	candidate = filepath.ToSlash(filepath.Clean(candidate))
	if info, err := os.Stat(filepath.Join(repoPath, candidate)); err == nil && info.IsDir() {
		return candidate, true
	}
	parent := filepath.ToSlash(filepath.Dir(candidate))
	if info, err := os.Stat(filepath.Join(repoPath, parent)); err == nil && info.IsDir() {
		return parent, true
	}
	return "", false
}

// goModuleName reads the module path from repoPath/go.mod, or "" if absent.
func goModuleName(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}
