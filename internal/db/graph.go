package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Node kinds.
const (
	KindPackage = "package"
	KindFile    = "file"
	KindFunc    = "func"
	KindMethod  = "method"
	KindType    = "type"
	KindConst   = "const"
)

// Edge kinds.
const (
	EdgeDefines    = "defines"
	EdgeImports    = "imports"
	EdgeCalls      = "calls"
	EdgeImplements = "implements"
	EdgeEmbeds     = "embeds"
)

// Node is one row of the nodes table: a package, file, or symbol
// (func/method/type/const).
type Node struct {
	ID      string
	Kind    string
	Name    string
	File    string
	Line    int
	Summary string
}

// Edge is one row of the edges table.
type Edge struct {
	Src  string
	Dst  string
	Kind string
}

// metaGraphVersion is the meta key holding a monotonically increasing
// counter bumped on every node/edge write, so `nav graph summary` knows
// whether its cached digest is stale without diffing the whole graph.
const metaGraphVersion = "graph_version"

// UpsertNode writes or replaces a node row.
func UpsertNode(exec Execer, n Node) error {
	_, err := exec.Exec(`
		INSERT INTO nodes (id, kind, name, file, line, summary)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			name = excluded.name,
			file = excluded.file,
			line = excluded.line,
			summary = excluded.summary
	`, n.ID, n.Kind, n.Name, n.File, n.Line, n.Summary)
	if err != nil {
		return fmt.Errorf("upserting node %q: %w", n.ID, err)
	}
	return bumpGraphVersion(exec)
}

// DeleteNode removes a node row by id.
func DeleteNode(exec Execer, id string) error {
	if _, err := exec.Exec(`DELETE FROM nodes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting node %q: %w", id, err)
	}
	return bumpGraphVersion(exec)
}

// DeleteOutgoingEdges removes every edge whose src is id, ahead of
// re-extracting a dirty symbol's edges from scratch.
func DeleteOutgoingEdges(exec Execer, src string) error {
	if _, err := exec.Exec(`DELETE FROM edges WHERE src = ?`, src); err != nil {
		return fmt.Errorf("deleting outgoing edges for %q: %w", src, err)
	}
	return bumpGraphVersion(exec)
}

// DeleteOutgoingEdgesByKind removes every edge of the given kind whose src is
// id, ahead of re-extracting that one edge kind from scratch. Used for a
// file's "imports" edges, which are re-resolved on every sync regardless of
// whether any single symbol in the file is dirty (there's no cheap "did
// imports change" signal short of re-parsing them), without disturbing the
// file's "defines" edges to symbols that weren't touched.
func DeleteOutgoingEdgesByKind(exec Execer, src, kind string) error {
	if _, err := exec.Exec(`DELETE FROM edges WHERE src = ? AND kind = ?`, src, kind); err != nil {
		return fmt.Errorf("deleting outgoing %s edges for %q: %w", kind, src, err)
	}
	return bumpGraphVersion(exec)
}

// InsertEdge adds an edge, silently doing nothing if it already exists
// (edges are pure facts, re-asserting one is a no-op).
func InsertEdge(exec Execer, e Edge) error {
	_, err := exec.Exec(`INSERT OR IGNORE INTO edges (src, dst, kind) VALUES (?, ?, ?)`, e.Src, e.Dst, e.Kind)
	if err != nil {
		return fmt.Errorf("inserting edge %s -%s-> %s: %w", e.Src, e.Kind, e.Dst, err)
	}
	return bumpGraphVersion(exec)
}

func bumpGraphVersion(exec Execer) error {
	current, ok, err := getMeta(exec, metaGraphVersion)
	if err != nil {
		return err
	}
	v := int64(0)
	if ok {
		v, _ = strconv.ParseInt(current, 10, 64)
	}
	return SetMeta(exec, metaGraphVersion, strconv.FormatInt(v+1, 10))
}

// GraphVersion returns the current graph_version counter.
func GraphVersion(exec Execer) (int64, error) {
	current, ok, err := getMeta(exec, metaGraphVersion)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	v, _ := strconv.ParseInt(current, 10, 64)
	return v, nil
}

// NodeByID returns the node with the given id, or ok=false if absent.
func NodeByID(exec Execer, id string) (Node, bool, error) {
	var n Node
	err := exec.QueryRow(`SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'') FROM nodes WHERE id = ?`, id).
		Scan(&n.ID, &n.Kind, &n.Name, &n.File, &n.Line, &n.Summary)
	if err == sql.ErrNoRows {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, fmt.Errorf("querying node %q: %w", id, err)
	}
	return n, true, nil
}

// NodesByName returns every node (any kind) whose name matches exactly —
// symbol names are not guaranteed unique across files/packages.
func NodesByName(exec Execer, name string) ([]Node, error) {
	rows, err := exec.Query(`
		SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'')
		FROM nodes WHERE name = ? ORDER BY kind, file
	`, name)
	if err != nil {
		return nil, fmt.Errorf("querying nodes named %q: %w", name, err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// NodesByNames returns every node (any kind) whose name exactly matches one
// of names — a batched form of NodesByName for looking up several candidate
// identifiers (e.g. tokens pulled from a free-text query) in one round trip.
func NodesByNames(exec Execer, names []string) ([]Node, error) {
	if len(names) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(names))
	args := make([]interface{}, len(names))
	for i, name := range names {
		placeholders[i] = "?"
		args[i] = name
	}
	rows, err := exec.Query(fmt.Sprintf(`
		SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'')
		FROM nodes WHERE name IN (%s) ORDER BY kind, file
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("querying nodes named %v: %w", names, err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// NodesInFile returns every node whose file column is file — the file node
// itself plus every symbol node defined in it (package nodes are never
// file-scoped, so they are never returned here). Used to tear down a file's
// old graph state before re-extracting it from a fresh parse.
func NodesInFile(exec Execer, file string) ([]Node, error) {
	rows, err := exec.Query(`
		SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'')
		FROM nodes WHERE file = ?
	`, file)
	if err != nil {
		return nil, fmt.Errorf("querying nodes in file %q: %w", file, err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// SymbolNodes returns every func/method/type/const node — i.e. every node
// that isn't a package or a file.
func SymbolNodes(exec Execer) ([]Node, error) {
	rows, err := exec.Query(`
		SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'')
		FROM nodes WHERE kind IN (?, ?, ?, ?) ORDER BY file, line
	`, KindFunc, KindMethod, KindType, KindConst)
	if err != nil {
		return nil, fmt.Errorf("querying symbol nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// FileNodes returns every file node.
func FileNodes(exec Execer) ([]Node, error) {
	rows, err := exec.Query(`
		SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'')
		FROM nodes WHERE kind = ? ORDER BY file
	`, KindFile)
	if err != nil {
		return nil, fmt.Errorf("querying file nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// Packages returns every package node, ordered by name.
func Packages(exec Execer) ([]Node, error) {
	rows, err := exec.Query(`
		SELECT id, kind, name, COALESCE(file,''), COALESCE(line,0), COALESCE(summary,'')
		FROM nodes WHERE kind = ? ORDER BY name
	`, KindPackage)
	if err != nil {
		return nil, fmt.Errorf("querying packages: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// EdgesFrom returns every edge whose src is id.
func EdgesFrom(exec Execer, id string) ([]Edge, error) {
	return queryEdges(exec, `SELECT src, dst, kind FROM edges WHERE src = ? ORDER BY kind, dst`, id)
}

// EdgesTo returns every edge whose dst is id.
func EdgesTo(exec Execer, id string) ([]Edge, error) {
	return queryEdges(exec, `SELECT src, dst, kind FROM edges WHERE dst = ? ORDER BY kind, src`, id)
}

func queryEdges(exec Execer, query string, arg string) ([]Edge, error) {
	rows, err := exec.Query(query, arg)
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FanIn is a symbol node together with its incoming call count.
type FanIn struct {
	Node
	Count int
}

// TopFanIn returns the limit symbol nodes with the most incoming `calls`
// edges, most-called first.
func TopFanIn(exec Execer, limit int) ([]FanIn, error) {
	rows, err := exec.Query(`
		SELECT n.id, n.kind, n.name, COALESCE(n.file,''), COALESCE(n.line,0), COALESCE(n.summary,''), COUNT(*) AS c
		FROM edges e JOIN nodes n ON n.id = e.dst
		WHERE e.kind = ?
		GROUP BY e.dst
		ORDER BY c DESC, n.name
		LIMIT ?
	`, EdgeCalls, limit)
	if err != nil {
		return nil, fmt.Errorf("querying fan-in: %w", err)
	}
	defer rows.Close()

	var out []FanIn
	for rows.Next() {
		var f FanIn
		if err := rows.Scan(&f.ID, &f.Kind, &f.Name, &f.File, &f.Line, &f.Summary, &f.Count); err != nil {
			return nil, fmt.Errorf("scanning fan-in row: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// NodeDepth is a node paired with how many hops it is from the walk's roots.
type NodeDepth struct {
	Node
	Depth int
}

// Callers walks the `calls` edges backward (dst -> src) from rootIDs, up to
// maxDepth hops, and returns every distinct caller found (roots excluded).
func Callers(exec Execer, rootIDs []string, maxDepth int) ([]NodeDepth, error) {
	return walk(exec, rootIDs, EdgeCalls, false, maxDepth)
}

// Deps walks the `imports` edges forward (src -> dst) from rootIDs, up to
// maxDepth hops, and returns every distinct dependency found (roots excluded).
func Deps(exec Execer, rootIDs []string, maxDepth int) ([]NodeDepth, error) {
	return walk(exec, rootIDs, EdgeImports, true, maxDepth)
}

// walk performs a bounded-depth recursive-CTE traversal of edges of the given
// kind, starting from rootIDs. forward=true follows src->dst (used for
// "deps"); forward=false follows dst->src (used for "callers").
func walk(exec Execer, rootIDs []string, kind string, forward bool, maxDepth int) ([]NodeDepth, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	if maxDepth < 0 {
		maxDepth = 0
	}

	seedPlaceholders := make([]string, len(rootIDs))
	args := make([]interface{}, 0, len(rootIDs)+2)
	for i, id := range rootIDs {
		seedPlaceholders[i] = "(?, 0)"
		args = append(args, id)
	}

	join := "e.src = w.id" // forward: from the current id, follow edges where it is the src
	next := "e.dst"        // ...landing on the dst as the next id
	if !forward {
		join = "e.dst = w.id" // backward: from the current id, follow edges where it is the dst
		next = "e.src"        // ...landing on the src (the caller) as the next id
	}

	query := fmt.Sprintf(`
		WITH RECURSIVE walk(id, depth) AS (
			VALUES %s
			UNION
			SELECT %s, w.depth + 1
			FROM edges e JOIN walk w ON %s
			WHERE e.kind = ? AND w.depth < ?
		)
		SELECT DISTINCT n.id, n.kind, n.name, COALESCE(n.file,''), COALESCE(n.line,0), COALESCE(n.summary,''), walk.depth
		FROM walk JOIN nodes n ON n.id = walk.id
		WHERE walk.depth > 0
		ORDER BY walk.depth, n.name
	`, strings.Join(seedPlaceholders, ", "), next, join)

	args = append(args, kind, maxDepth)

	rows, err := exec.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("walking %s edges: %w", kind, err)
	}
	defer rows.Close()

	var out []NodeDepth
	for rows.Next() {
		var nd NodeDepth
		if err := rows.Scan(&nd.ID, &nd.Kind, &nd.Name, &nd.File, &nd.Line, &nd.Summary, &nd.Depth); err != nil {
			return nil, fmt.Errorf("scanning walk row: %w", err)
		}
		out = append(out, nd)
	}
	return out, rows.Err()
}

func scanNodes(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]Node, error) {
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.File, &n.Line, &n.Summary); err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
