package services

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"nav/internal/db"
	"nav/internal/hook"
)

// identifierPattern matches code-identifier-shaped tokens (letters, digits,
// underscore, starting with a letter or underscore) inside a free-text
// query, so GraphSearch can pull candidate symbol names — e.g. "HookSearch"
// out of a prompt like "why does HookSearch fail on an empty query".
var identifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// minIdentifierLen filters out short tokens ("a", "in", "on") that are
// overwhelmingly English filler rather than code identifiers. It's a cheap
// heuristic, not a guarantee — the real filter is that the token then has to
// exactly match an actual node name in the graph, and generic filler words
// essentially never do.
const minIdentifierLen = 3

// queryIdentifiers extracts the distinct identifier-shaped tokens of at
// least minIdentifierLen runes from query, in first-seen order.
func queryIdentifiers(query string) []string {
	matches := identifierPattern.FindAllString(query, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < minIdentifierLen || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// graphEdgeCap bounds how many outgoing/incoming edges GraphSearch prints
// per matched symbol, so one heavily-called helper (fan-in of dozens) can't
// blow a hook's token budget by itself.
const graphEdgeCap = 8

// GraphSearch is the fast, precise path HookSearch tries before falling
// back to Qdrant's semantic search: it extracts identifier-shaped tokens
// from query and looks each one up as an exact name match against the
// current branch's knowledge graph. An exact graph hit is strictly more
// trustworthy than a vector similarity score for the same symbol — it also
// carries real call-graph relationships (who it calls, who calls it), which
// a vector chunk alone doesn't — so results are scored 1 and, once
// combined with any semantic hits by the caller, naturally sort first.
// limit caps the number of results (<=0 means unlimited); nodes are
// returned in the order their token first appeared in query.
func GraphSearch(project, repoPath, query string, limit int) ([]hook.ContextResult, error) {
	sdb, err := openProjectDB(project, repoPath, CurrentBranch(repoPath))
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	defer sdb.Close()
	return graphSearch(sdb, query, limit)
}

func graphSearch(sdb *db.DB, query string, limit int) ([]hook.ContextResult, error) {
	tokens := queryIdentifiers(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	nodes, err := db.NodesByNames(sdb, tokens)
	if err != nil {
		return nil, err
	}
	nodes = filterSymbolNodes(nodes)
	if len(nodes) == 0 {
		return nil, nil
	}

	order := make(map[string]int, len(tokens))
	for i, t := range tokens {
		order[t] = i
	}
	sort.SliceStable(nodes, func(i, j int) bool { return order[nodes[i].Name] < order[nodes[j].Name] })

	if limit > 0 && len(nodes) > limit {
		nodes = nodes[:limit]
	}

	results := make([]hook.ContextResult, 0, len(nodes))
	for _, n := range nodes {
		out, err := db.EdgesFrom(sdb, n.ID)
		if err != nil {
			return nil, err
		}
		in, err := db.EdgesTo(sdb, n.ID)
		if err != nil {
			return nil, err
		}

		results = append(results, hook.ContextResult{
			Score:   1,
			Symbol:  n.Name,
			Type:    n.Kind,
			File:    n.File,
			Purpose: graphPurpose(n),
			Code:    graphEdgesBlock(out, in),
		})
	}
	return results, nil
}

// graphPurpose renders a symbol's stored summary alongside its definition
// site, so a graph hit carries the same "where is it" precision as `nav
// graph symbol` even though ContextResult has no dedicated line field.
func graphPurpose(n db.Node) string {
	if n.Summary == "" {
		return fmt.Sprintf("defined at %s:%d", n.File, n.Line)
	}
	return fmt.Sprintf("%s (defined at %s:%d)", n.Summary, n.File, n.Line)
}

// graphEdgesBlock renders a symbol's direct call-graph edges — the extra
// relationship context a graph hit has over a plain vector chunk — capped
// at graphEdgeCap per direction, in the same "-kind-> id" shorthand as `nav
// graph symbol`.
func graphEdgesBlock(out, in []db.Edge) string {
	var b strings.Builder
	if len(out) > 0 {
		b.WriteString("outgoing:\n")
		for _, e := range capEdges(out) {
			fmt.Fprintf(&b, "  -%s-> %s\n", e.Kind, e.Dst)
		}
	}
	if len(in) > 0 {
		b.WriteString("incoming:\n")
		for _, e := range capEdges(in) {
			fmt.Fprintf(&b, "  %s -%s->\n", e.Src, e.Kind)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func capEdges(edges []db.Edge) []db.Edge {
	if len(edges) > graphEdgeCap {
		return edges[:graphEdgeCap]
	}
	return edges
}
