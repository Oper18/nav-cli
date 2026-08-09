package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"nav/internal/db"
	"nav/internal/parser"
)

// graphSummaryCharBudget caps `nav graph summary` at roughly 1000 tokens
// (~4 chars/token), matching the digest's purpose: a quick LLM-consumable
// orientation, not a full report.
const graphSummaryCharBudget = 4000

const (
	metaGraphDigest        = "graph_digest"
	metaGraphDigestVersion = "graph_digest_version"
)

// GraphSummaryDigest opens the current branch's knowledge graph and returns
// the ~1000-token digest: packages, entry points, top-called symbols.
func GraphSummaryDigest(project, repoPath string) (string, error) {
	sdb, err := openProjectDB(project, repoPath, CurrentBranch(repoPath))
	if err != nil {
		return "", fmt.Errorf("opening db: %w", err)
	}
	defer sdb.Close()
	return graphSummaryDigest(sdb)
}

// graphSummaryDigest returns the cached digest when nothing has changed
// since it was last built (meta.graph_digest_version == the live
// graph_version), and rebuilds it otherwise. Read from SQLite, not
// re-derived from source — the whole point of the graph living in
// internal/db is that this is a handful of indexed queries, not a repo
// walk.
func graphSummaryDigest(sdb *db.DB) (string, error) {
	current, err := db.GraphVersion(sdb)
	if err != nil {
		return "", err
	}

	if cachedVersion, ok, err := sdb.GetMeta(metaGraphDigestVersion); err != nil {
		return "", err
	} else if ok {
		if v, _ := strconv.ParseInt(cachedVersion, 10, 64); v == current {
			if digest, ok2, err := sdb.GetMeta(metaGraphDigest); err != nil {
				return "", err
			} else if ok2 {
				return digest, nil
			}
		}
	}

	digest, err := renderGraphSummary(sdb)
	if err != nil {
		return "", err
	}
	if err := sdb.SetMeta(metaGraphDigestVersion, strconv.FormatInt(current, 10)); err != nil {
		return "", err
	}
	if err := sdb.SetMeta(metaGraphDigest, digest); err != nil {
		return "", err
	}
	return digest, nil
}

func renderGraphSummary(sdb *db.DB) (string, error) {
	packages, err := db.Packages(sdb)
	if err != nil {
		return "", err
	}
	symbols, err := db.SymbolNodes(sdb)
	if err != nil {
		return "", err
	}
	fanIn, err := db.TopFanIn(sdb, 10)
	if err != nil {
		return "", err
	}

	byPkg := make(map[string][]db.Node)
	for _, s := range symbols {
		dir := parser.PackageDir(s.File)
		byPkg[dir] = append(byPkg[dir], s)
	}

	var b strings.Builder
	b.WriteString("# Codebase graph summary (nav graph summary)\n")

	b.WriteString("\n## Packages\n")
	local := 0
	for _, p := range packages {
		if strings.HasPrefix(p.ID, "pkg:ext:") {
			continue // external (stdlib/third-party) packages aren't part of "this codebase"
		}
		local++
		syms := byPkg[p.Name]
		fmt.Fprintf(&b, "- %s (%d symbols)", p.Name, len(syms))
		if line := packageOneLiner(syms); line != "" {
			fmt.Fprintf(&b, " — %s", line)
		}
		b.WriteString("\n")
	}
	if local == 0 {
		b.WriteString("(none indexed yet — run `nav sync`)\n")
	}

	var entryPoints []db.Node
	for _, s := range symbols {
		if s.Kind == db.KindFunc && strings.EqualFold(s.Name, "main") {
			entryPoints = append(entryPoints, s)
		}
	}
	if len(entryPoints) > 0 {
		b.WriteString("\n## Entry points\n")
		for _, e := range entryPoints {
			fmt.Fprintf(&b, "- %s:%d %s\n", e.File, e.Line, e.Name)
		}
	}

	if len(fanIn) > 0 {
		b.WriteString("\n## Top called symbols (fan-in)\n")
		for i, f := range fanIn {
			fmt.Fprintf(&b, "%d. %s (%d callers) — %s:%d\n", i+1, f.Name, f.Count, f.File, f.Line)
		}
	}

	return capRunes(b.String(), graphSummaryCharBudget), nil
}

// packageOneLiner synthesizes a package's responsibility line from up to two
// of its symbols' already-computed LLM summaries — no new LLM calls, purely
// reusing what a prior sync generated.
func packageOneLiner(symbols []db.Node) string {
	var parts []string
	for _, s := range symbols {
		if s.Summary == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", s.Name, s.Summary))
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

// projectStructureCharBudget caps the full project-structure digest at
// roughly 8000 tokens (~4 chars/token) — generous enough to cover the
// complete layout of most repositories, unlike GraphSummaryDigest's tight
// budget, while still guarding against unbounded output on a huge monorepo.
const projectStructureCharBudget = 32000

const (
	metaStructureDigest        = "structure_digest"
	metaStructureDigestVersion = "structure_digest_version"
)

// ProjectStructureDigest opens the current branch's knowledge graph and
// renders the complete package/file tree — every indexed package and every
// file it contains. Unlike GraphSummaryDigest's tight, curated orientation,
// this is deliberately exhaustive: the point is to hand an assistant the
// codebase's full layout up front, straight from the graph, so it never
// needs to re-discover it with find/ls/tree at the start of a session.
func ProjectStructureDigest(project, repoPath string) (string, error) {
	sdb, err := openProjectDB(project, repoPath, CurrentBranch(repoPath))
	if err != nil {
		return "", fmt.Errorf("opening db: %w", err)
	}
	defer sdb.Close()
	return projectStructureDigest(sdb)
}

// projectStructureDigest returns the cached digest when nothing has changed
// since it was last built (meta.structure_digest_version == the live
// graph_version), and rebuilds it otherwise — the same cache-by-graph-
// version pattern as graphSummaryDigest.
func projectStructureDigest(sdb *db.DB) (string, error) {
	current, err := db.GraphVersion(sdb)
	if err != nil {
		return "", err
	}

	if cachedVersion, ok, err := sdb.GetMeta(metaStructureDigestVersion); err != nil {
		return "", err
	} else if ok {
		if v, _ := strconv.ParseInt(cachedVersion, 10, 64); v == current {
			if digest, ok2, err := sdb.GetMeta(metaStructureDigest); err != nil {
				return "", err
			} else if ok2 {
				return digest, nil
			}
		}
	}

	digest, err := renderProjectStructure(sdb)
	if err != nil {
		return "", err
	}
	if err := sdb.SetMeta(metaStructureDigestVersion, strconv.FormatInt(current, 10)); err != nil {
		return "", err
	}
	if err := sdb.SetMeta(metaStructureDigest, digest); err != nil {
		return "", err
	}
	return digest, nil
}

// renderProjectStructure lists every local package directory together with
// every file node it contains (and each file's symbol count, when it has
// any), in package/file path order. Grouping and ordering both come
// straight from the already-sorted db.Packages/db.FileNodes queries, so no
// extra sort is needed here.
func renderProjectStructure(sdb *db.DB) (string, error) {
	packages, err := db.Packages(sdb)
	if err != nil {
		return "", err
	}
	files, err := db.FileNodes(sdb)
	if err != nil {
		return "", err
	}
	symbols, err := db.SymbolNodes(sdb)
	if err != nil {
		return "", err
	}

	symbolCountByFile := make(map[string]int, len(symbols))
	for _, s := range symbols {
		symbolCountByFile[s.File]++
	}

	filesByPkg := make(map[string][]db.Node)
	for _, f := range files {
		dir := parser.PackageDir(f.File)
		filesByPkg[dir] = append(filesByPkg[dir], f)
	}

	var b strings.Builder
	b.WriteString("# Project structure (nav graph structure)\n\n")

	local := 0
	for _, p := range packages {
		if strings.HasPrefix(p.ID, "pkg:ext:") {
			continue // external (stdlib/third-party) packages aren't part of "this codebase"
		}
		local++

		label := p.Name
		if label == "." {
			label = "(root)"
		}
		fmt.Fprintf(&b, "%s/\n", label)

		for _, f := range filesByPkg[p.Name] {
			if n := symbolCountByFile[f.File]; n > 0 {
				fmt.Fprintf(&b, "  %s (%d symbols)\n", f.Name, n)
			} else {
				fmt.Fprintf(&b, "  %s\n", f.Name)
			}
		}
	}
	if local == 0 {
		b.WriteString("(none indexed yet — run `nav sync`)\n")
	}

	return capRunes(b.String(), projectStructureCharBudget), nil
}

// SessionStartDigest returns the context injected into an assistant's
// SessionStart hook: the compact graph summary (packages, entry points,
// fan-in) followed by the complete project structure (every package and the
// files it contains). Handing over the full layout up front means the
// assistant can navigate the codebase from the graph nav sync already
// built, instead of re-parsing the project from scratch at the start of
// every session.
func SessionStartDigest(project, repoPath string) (string, error) {
	summary, err := GraphSummaryDigest(project, repoPath)
	if err != nil {
		return "", err
	}
	structure, err := ProjectStructureDigest(project, repoPath)
	if err != nil {
		return "", err
	}
	return summary + "\n\n" + structure, nil
}

// GraphCallers opens the current branch's knowledge graph and walks the
// `calls` edges backward from every symbol node named symbolName, up to
// depth hops. roots is empty when no symbol with that name exists.
func GraphCallers(project, repoPath, symbolName string, depth int) (roots []db.Node, results []db.NodeDepth, err error) {
	sdb, err := openProjectDB(project, repoPath, CurrentBranch(repoPath))
	if err != nil {
		return nil, nil, fmt.Errorf("opening db: %w", err)
	}
	defer sdb.Close()

	roots, err = db.NodesByName(sdb, symbolName)
	if err != nil {
		return nil, nil, err
	}
	roots = filterSymbolNodes(roots)
	if len(roots) == 0 {
		return roots, nil, nil
	}

	results, err = db.Callers(sdb, nodeIDs(roots), depth)
	if err != nil {
		return nil, nil, err
	}
	return roots, results, nil
}

// GraphDeps opens the current branch's knowledge graph and resolves target
// (a package directory or file path) to a node, then walks its `imports`
// edges forward up to depth hops. rootID is "" when target could not be
// resolved.
func GraphDeps(project, repoPath, target string, depth int) (rootID string, node db.Node, results []db.NodeDepth, err error) {
	sdb, err := openProjectDB(project, repoPath, CurrentBranch(repoPath))
	if err != nil {
		return "", db.Node{}, nil, fmt.Errorf("opening db: %w", err)
	}
	defer sdb.Close()

	rootID, node, err = resolvePackageOrFile(sdb, target)
	if err != nil {
		return "", db.Node{}, nil, err
	}
	if rootID == "" {
		return "", db.Node{}, nil, nil
	}

	results, err = packageDeps(sdb, rootID, depth)
	if err != nil {
		return "", db.Node{}, nil, err
	}
	return rootID, node, results, nil
}

// SymbolInfo pairs a symbol node with its direct outgoing/incoming edges.
type SymbolInfo struct {
	Node db.Node
	Out  []db.Edge
	In   []db.Edge
}

// GraphSymbol opens the current branch's knowledge graph and returns every
// func/method/type/const node named name, together with its direct edges.
func GraphSymbol(project, repoPath, name string) ([]SymbolInfo, error) {
	sdb, err := openProjectDB(project, repoPath, CurrentBranch(repoPath))
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	defer sdb.Close()

	nodes, err := db.NodesByName(sdb, name)
	if err != nil {
		return nil, err
	}
	nodes = filterSymbolNodes(nodes)

	infos := make([]SymbolInfo, 0, len(nodes))
	for _, n := range nodes {
		out, err := db.EdgesFrom(sdb, n.ID)
		if err != nil {
			return nil, err
		}
		in, err := db.EdgesTo(sdb, n.ID)
		if err != nil {
			return nil, err
		}
		infos = append(infos, SymbolInfo{Node: n, Out: out, In: in})
	}
	return infos, nil
}

// resolvePackageOrFile resolves a user-supplied "internal/db" or
// "internal/db/graph.go"-style argument to a graph node, trying it first
// as a file, then as a package directory, then as a literal node id (for
// callers that already know the exact "pkg:..."/"file:..." form).
func resolvePackageOrFile(sdb *db.DB, target string) (string, db.Node, error) {
	for _, id := range []string{parser.FileNodeID(target), parser.PackageNodeID(target), target} {
		n, ok, err := db.NodeByID(sdb, id)
		if err != nil {
			return "", db.Node{}, err
		}
		if ok {
			return id, n, nil
		}
	}
	return "", db.Node{}, nil
}

// packageDeps walks import edges forward from rootID up to maxDepth hops,
// returning every distinct package/file dependency found. It does its own
// breadth-first walk in Go rather than a SQL recursive CTE because `imports`
// edges are recorded per file (file --imports--> package), not per package —
// a package node itself carries none, so a pure edge-to-edge CTE walk would
// dead-end after depth 1 every time it lands on a package. Expanding a
// package root to its member files at each step is what lets multi-hop
// package dependency chains resolve correctly.
func packageDeps(sdb *db.DB, rootID string, maxDepth int) ([]db.NodeDepth, error) {
	visited := map[string]int{rootID: 0}
	frontier := []string{rootID}

	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, id := range frontier {
			fileIDs, err := filesToExpand(sdb, id)
			if err != nil {
				return nil, err
			}
			for _, fid := range fileIDs {
				edges, err := db.EdgesFrom(sdb, fid)
				if err != nil {
					return nil, err
				}
				for _, e := range edges {
					if e.Kind != db.EdgeImports {
						continue
					}
					if _, seen := visited[e.Dst]; seen {
						continue
					}
					visited[e.Dst] = depth
					next = append(next, e.Dst)
				}
			}
		}
		frontier = next
	}

	var results []db.NodeDepth
	for id, depth := range visited {
		if depth == 0 {
			continue // the root itself
		}
		n, ok, err := db.NodeByID(sdb, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		results = append(results, db.NodeDepth{Node: n, Depth: depth})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Depth != results[j].Depth {
			return results[i].Depth < results[j].Depth
		}
		return results[i].Name < results[j].Name
	})
	return results, nil
}

// filesToExpand returns the file node ids whose own `imports` edges should
// be followed for id: id itself when it's already a file, every file in the
// package when it's a local package, or nothing for an external package
// (whose files, if any, aren't ours to see).
func filesToExpand(sdb *db.DB, id string) ([]string, error) {
	if strings.HasPrefix(id, "file:") {
		return []string{id}, nil
	}
	if !isLocalPkgID(id) {
		return nil, nil
	}
	dir := strings.TrimPrefix(id, "pkg:")
	files, err := db.FileNodes(sdb)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range files {
		if parser.PackageDir(f.File) == dir {
			ids = append(ids, f.ID)
		}
	}
	return ids, nil
}

// filterSymbolNodes keeps only func/method/type/const nodes, dropping any
// package/file whose name happens to collide with the query.
func filterSymbolNodes(nodes []db.Node) []db.Node {
	out := nodes[:0]
	for _, n := range nodes {
		switch n.Kind {
		case db.KindFunc, db.KindMethod, db.KindType, db.KindConst:
			out = append(out, n)
		}
	}
	return out
}

func nodeIDs(nodes []db.Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}
