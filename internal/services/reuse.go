package services

import (
	"context"
	"fmt"
	"os"
	"sort"

	"nav/internal/db"
	"nav/internal/db/qdrant"
	"nav/internal/parser"
)

// reuseEmbeddings copies already-computed summaries and vectors for dirty
// symbols from other branches' databases instead of regenerating them. A
// symbol qualifies when another branch's manifest holds the same file and
// symbol with an embedded hash equal to the symbol's current content hash.
// Sources are tried in order: the branch's recorded ancestors, then every
// other local branch nav has a database for. Reused symbols are moved to the
// front of diff.dirty, with their LLM-derived fields filled in; the returned
// points (already re-keyed to branch) are for the caller to upsert, and n is
// how many symbols were reused. Any failure just leaves symbols to be
// embedded normally.
func reuseEmbeddings(ctx context.Context, qc *db.Client, collection, project, repoPath, branch string, diff *symbolDiff) (points []qdrant.Point, n int) {
	if qc == nil || len(diff.dirty) == 0 {
		return nil, 0
	}

	pending := make(map[int]bool, len(diff.dirty))
	for i := range diff.dirty {
		pending[i] = true
	}
	reused := make(map[int]bool)

	for _, src := range reuseSourceBranches(project, repoPath, branch) {
		if len(pending) == 0 {
			break
		}
		matches, err := manifestMatches(project, src, diff, pending)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: reading %s manifest: %v\n", src, err)
			continue
		}
		var ids []string
		for _, rows := range matches {
			for _, r := range rows {
				ids = append(ids, r.ChunkID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		stored, err := qc.Retrieve(ctx, collection, ids)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: reusing %s embeddings: %v\n", src, err)
			continue
		}
		for idx, rows := range matches {
			got, ok := collectChunks(rows, stored)
			if !ok {
				continue
			}
			sym := &diff.dirty[idx]
			first := got[0].Payload
			sym.Summary = first.Summary
			sym.Tags = first.Tags
			sym.BusinessContext = first.BusinessContext
			sym.Responsibilities = first.Responsibilities
			for _, p := range got {
				p.ID = qdrant.ID(branch, sym.Symbol, p.Payload.ChunkNumber)
				p.Payload.Branch = branch
				points = append(points, p)
			}
			reused[idx] = true
			delete(pending, idx)
		}
	}

	if len(reused) == 0 {
		return nil, 0
	}
	ordered := make([]parser.Symbol, 0, len(diff.dirty))
	for i, s := range diff.dirty {
		if reused[i] {
			ordered = append(ordered, s)
		}
	}
	for i, s := range diff.dirty {
		if !reused[i] {
			ordered = append(ordered, s)
		}
	}
	copy(diff.dirty, ordered)
	return points, len(reused)
}

// reuseSourceBranches lists the branches whose databases may hold reusable
// embeddings for branch, nearest ancestors first.
func reuseSourceBranches(project, repoPath, branch string) []string {
	var out []string
	seen := map[string]bool{branch: true}
	add := func(b string) {
		if b == "" || seen[b] || !db.Exists(project, b) {
			return
		}
		seen[b] = true
		out = append(out, b)
	}

	if chain, err := BranchChain(project, repoPath, branch); err == nil {
		for _, b := range chain {
			add(b)
		}
	}
	if locals, err := LocalBranches(repoPath); err == nil {
		sort.Strings(locals)
		for _, b := range locals {
			add(b)
		}
	}
	return out
}

// manifestMatches returns, per pending dirty-symbol index, the src manifest
// rows whose embedded hash equals the symbol's current content hash.
func manifestMatches(project, src string, diff *symbolDiff, pending map[int]bool) (map[int][]db.Chunk, error) {
	sdb, err := db.Open(project, src)
	if err != nil {
		return nil, err
	}
	defer sdb.Close()

	byFile := make(map[string][]db.Chunk)
	matches := make(map[int][]db.Chunk)
	for idx := range pending {
		sym := diff.dirty[idx]
		rows, ok := byFile[sym.FilePath]
		if !ok {
			if rows, err = db.ChunksForFile(sdb, sym.FilePath); err != nil {
				return nil, err
			}
			byFile[sym.FilePath] = rows
		}
		want := diff.hashOf[symbolKey(sym.FilePath, sym.Symbol)]
		for _, r := range rows {
			if r.Symbol == sym.Symbol && r.EmbeddedHash == want {
				matches[idx] = append(matches[idx], r)
			}
		}
	}
	return matches, nil
}

// collectChunks returns the stored points for rows ordered by chunk number,
// or ok=false unless the symbol's complete chunk set is present.
func collectChunks(rows []db.Chunk, stored map[string]qdrant.Point) ([]qdrant.Point, bool) {
	got := make([]qdrant.Point, 0, len(rows))
	for _, r := range rows {
		p, ok := stored[r.ChunkID]
		if !ok || len(p.Vector) == 0 {
			return nil, false
		}
		got = append(got, p)
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Payload.ChunkNumber < got[j].Payload.ChunkNumber
	})
	for i, p := range got {
		if p.Payload.ChunkNumber != i || p.Payload.ChunkCount != len(got) {
			return nil, false
		}
	}
	return got, true
}
