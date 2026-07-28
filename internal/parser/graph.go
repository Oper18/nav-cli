package parser

import (
	"path/filepath"
	"regexp"
	"strings"
)

// GraphNode and GraphEdge mirror internal/db's Node/Edge row shapes
// field-for-field. They live here (rather than importing internal/db) so
// this package stays dependency-free; callers that persist them do a 1:1
// struct copy.
type GraphNode struct {
	ID      string
	Kind    string
	Name    string
	File    string
	Line    int
	Summary string
}

type GraphEdge struct {
	Src  string
	Dst  string
	Kind string
}

// Node/edge kind strings, matching the schema documented in
// internal/db/graph.go. Duplicated as literals (not imported constants)
// to keep parser independent of the storage layer.
const (
	nodeKindPackage = "package"
	nodeKindFile    = "file"
	nodeKindFunc    = "func"
	nodeKindMethod  = "method"
	nodeKindType    = "type"
	nodeKindConst   = "const"

	edgeDefines    = "defines"
	edgeEmbeds     = "embeds"
	edgeImplements = "implements"
)

// PackageDir returns the language-agnostic stand-in for "package": the
// directory containing relPath, relative to the repo root, or "." for a
// root-level file.
func PackageDir(relPath string) string {
	dir := filepath.ToSlash(filepath.Dir(relPath))
	if dir == "" {
		return "."
	}
	return dir
}

// PackageNodeID returns the node id for a local (in-repo) package directory.
func PackageNodeID(dir string) string { return "pkg:" + dir }

// ExternalPackageNodeID returns the node id for a package/module that could
// not be resolved to a directory inside the repo (stdlib, third-party, or
// unresolvable relative import).
func ExternalPackageNodeID(importPath string) string { return "pkg:ext:" + importPath }

// FileNodeID returns the node id for a file.
func FileNodeID(relPath string) string { return "file:" + relPath }

// SymbolNodeID returns the node id for a symbol defined in relPath. Symbol
// names are qualified with their receiver already (e.g. "Client.Close"), so
// this alone disambiguates functions from methods of the same name.
func SymbolNodeID(relPath, symbol string) string { return "sym:" + relPath + "#" + symbol }

// NodeKind maps a parser symbol Type (as produced by ParseFile: "function",
// "method", "struct", "class", "interface", "enum", "trait", "const") to the
// graph's coarser kind enum.
func NodeKind(symType string) string {
	switch symType {
	case "function":
		return nodeKindFunc
	case "method":
		return nodeKindMethod
	case "const":
		return nodeKindConst
	default:
		// struct/class/interface/enum/trait all collapse to "type" — the
		// schema's kind enum does not distinguish them further.
		return nodeKindType
	}
}

// summaryClip bounds how much of a symbol's LLM-generated summary is copied
// onto its graph node — `nav graph summary` only needs a one-line hook, not
// the full prose, and the digest itself is budgeted to ~1000 tokens.
const summaryClip = 160

func clipSummary(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	r := []rune(s)
	if len(r) <= summaryClip {
		return s
	}
	return string(r[:summaryClip]) + "…"
}

// BuildFileNodes produces the package/file/symbol nodes and their purely
// structural edges (defines, plus the Go-only embeds/implements heuristics)
// for one file. It needs nothing but this file's already-extracted symbols —
// no filesystem or database access — because "defines" and "embeds" are
// self-contained facts about the file. Cross-file facts (imports, calls) are
// resolved separately by the caller against the persistent graph in
// internal/db, since that requires knowledge this single file doesn't
// have.
func BuildFileNodes(relPath string, symbols []Symbol) ([]GraphNode, []GraphEdge) {
	dir := PackageDir(relPath)
	pkgID := PackageNodeID(dir)
	fileID := FileNodeID(relPath)

	nodes := []GraphNode{
		{ID: pkgID, Kind: nodeKindPackage, Name: dir},
		{ID: fileID, Kind: nodeKindFile, Name: filepath.Base(relPath), File: relPath},
	}
	edges := []GraphEdge{
		{Src: pkgID, Dst: fileID, Kind: edgeDefines},
	}

	for _, sym := range symbols {
		id := SymbolNodeID(relPath, sym.Symbol)
		nodes = append(nodes, GraphNode{
			ID:      id,
			Kind:    NodeKind(sym.Type),
			Name:    sym.Symbol,
			File:    relPath,
			Line:    int(sym.StartLine),
			Summary: clipSummary(sym.Summary),
		})
		edges = append(edges, GraphEdge{Src: fileID, Dst: id, Kind: edgeDefines})
	}

	if lang := ProgrammingLanguage(relPath); lang == "go" {
		edges = append(edges, goEmbedsEdges(relPath, symbols)...)
		edges = append(edges, goImplementsEdges(relPath, symbols)...)
	}

	return nodes, edges
}

// goStructFieldLine matches a struct body line that is a single bare (or
// pointer, or package-qualified) type identifier with no field name — i.e.
// an embedded field, such as "sync.Mutex" or "*Base".
var goStructFieldLine = regexp.MustCompile(`^\*?[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// goEmbedsEdges detects Go struct embedding by line-scanning each struct
// symbol's own source in this file: a struct body line consisting solely of
// a type name (optionally pointer/qualified) with no following field name is
// an embedded field. This only looks at struct bodies already captured in
// Symbol.Content, so it is inherently same-file/same-symbol — no guessing
// about types defined elsewhere.
func goEmbedsEdges(relPath string, symbols []Symbol) []GraphEdge {
	var edges []GraphEdge
	for _, sym := range symbols {
		if sym.Type != "struct" {
			continue
		}
		structID := SymbolNodeID(relPath, sym.Symbol)
		for _, line := range strings.Split(sym.Content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = strings.TrimSpace(line[:idx])
			}
			if idx := strings.Index(line, "`"); idx >= 0 {
				line = strings.TrimSpace(line[:idx])
			}
			line = strings.TrimSuffix(line, ",")
			fields := strings.Fields(line)
			if len(fields) != 1 || !goStructFieldLine.MatchString(fields[0]) {
				continue
			}
			embedded := strings.TrimPrefix(fields[0], "*")
			// Unqualified embeds resolve to a symbol defined in this same
			// file when one exists; qualified (pkg.Type) embeds of a type we
			// haven't indexed are recorded against a synthetic node id so
			// the edge still carries the name, without inventing a fake
			// local definition for it.
			dst := SymbolNodeID(relPath, embedded)
			if !hasSymbol(symbols, embedded) {
				dst = "sym:ext:" + embedded
			}
			edges = append(edges, GraphEdge{Src: structID, Dst: dst, Kind: edgeEmbeds})
		}
	}
	return edges
}

// goInterfaceMethodLine matches an interface body line declaring a method:
// a bare identifier immediately followed by a parameter list.
var goInterfaceMethodLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(`)

// goImplementsEdges is a best-effort, Go-only, same-file heuristic: for each
// interface defined in this file, collect its required method names by
// line-scanning the interface body, then check every struct/type in this
// same file whose same-file method set (Type=="method" symbols whose
// receiver matches the type's name) is a superset. This is not real type
// checking — it cannot see methods defined in other files of the same
// package, and structural interface satisfaction in Go is a property of the
// whole package, not one file — so it will under-detect. It never guesses
// beyond that literal method-set comparison.
func goImplementsEdges(relPath string, symbols []Symbol) []GraphEdge {
	var interfaces []Symbol
	methodsByReceiver := make(map[string]map[string]bool)
	typeNames := make(map[string]bool)

	for _, sym := range symbols {
		switch sym.Type {
		case "interface":
			interfaces = append(interfaces, sym)
		case "struct", "class":
			typeNames[sym.Symbol] = true
		case "method":
			recv, method := splitReceiver(sym.Symbol)
			if recv == "" {
				continue
			}
			if methodsByReceiver[recv] == nil {
				methodsByReceiver[recv] = make(map[string]bool)
			}
			methodsByReceiver[recv][method] = true
		}
	}

	if len(interfaces) == 0 || len(typeNames) == 0 {
		return nil
	}

	var edges []GraphEdge
	for _, iface := range interfaces {
		required := goInterfaceMethods(iface.Content)
		if len(required) == 0 {
			continue // skip the empty interface{} and any we failed to parse
		}
		for typeName := range typeNames {
			have := methodsByReceiver[typeName]
			if len(have) == 0 {
				continue
			}
			satisfies := true
			for _, m := range required {
				if !have[m] {
					satisfies = false
					break
				}
			}
			if satisfies {
				edges = append(edges, GraphEdge{
					Src:  SymbolNodeID(relPath, typeName),
					Dst:  SymbolNodeID(relPath, iface.Symbol),
					Kind: edgeImplements,
				})
			}
		}
	}
	return edges
}

// goInterfaceMethods line-scans an interface's raw source for method
// signatures, returning their bare names.
func goInterfaceMethods(content string) []string {
	var methods []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		m := goInterfaceMethodLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		methods = append(methods, m[1])
	}
	return methods
}

// splitReceiver splits a qualified symbol name like "Client.Close" into its
// receiver type and bare method name. Returns "" for the receiver when
// symbol carries no qualification.
func splitReceiver(symbol string) (receiver, method string) {
	idx := strings.LastIndex(symbol, ".")
	if idx < 0 {
		return "", symbol
	}
	return symbol[:idx], symbol[idx+1:]
}

func hasSymbol(symbols []Symbol, name string) bool {
	for _, s := range symbols {
		if s.Symbol == name {
			return true
		}
	}
	return false
}

// ImportRef is one import statement's alias (the identifier code uses to
// refer to it) and its raw path/module string, as written in source.
type ImportRef struct {
	Alias string
	Path  string
}

var (
	reGoImport     = regexp.MustCompile(`(?m)^\s*(?:(\w+)\s+)?"([^"]+)"\s*$`)
	reJSNamespace  = regexp.MustCompile(`import\s+\*\s+as\s+(\w+)\s+from\s+["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	reJSDefault    = regexp.MustCompile(`import\s+(\w+)\s*(?:,\s*\{[^}]*\})?\s+from\s+["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	reJSNamed      = regexp.MustCompile(`import\s+\{([^}]*)\}\s+from\s+["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	rePyImport     = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+)(?:\s+as\s+(\w+))?`)
	rePyFromImport = regexp.MustCompile(`(?m)^\s*from\s+([\w.]+)\s+import\s+(.+)$`)
)

// ExtractFileImports returns the imports declared in source, resolved to
// (alias, path) pairs so call-site qualifiers ("alias.Symbol" or, for
// "from X import Y", the bare name "Y") can later be mapped back to the
// package they came from. It is intentionally best-effort: languages it
// cannot confidently parse simply yield no imports rather than guessing.
func ExtractFileImports(lang string, source []byte) []ImportRef {
	src := string(source)
	var refs []ImportRef

	switch lang {
	case LangGo:
		for _, m := range reGoImport.FindAllStringSubmatch(src, -1) {
			path := m[2]
			alias := m[1]
			if alias == "" {
				alias = lastSegment(path, "/")
			}
			if alias == "_" || alias == "." {
				continue // blank/dot imports don't introduce a resolvable alias
			}
			refs = append(refs, ImportRef{Alias: alias, Path: path})
		}

	case LangTypeScript, LangJavaScript:
		for _, m := range reJSNamespace.FindAllStringSubmatch(src, -1) {
			refs = append(refs, ImportRef{Alias: m[1], Path: m[2]})
		}
		for _, m := range reJSDefault.FindAllStringSubmatch(src, -1) {
			refs = append(refs, ImportRef{Alias: m[1], Path: m[2]})
		}
		for _, m := range reJSNamed.FindAllStringSubmatch(src, -1) {
			path := m[2]
			for _, name := range strings.Split(m[1], ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				alias := name
				if idx := strings.Index(name, " as "); idx >= 0 {
					alias = strings.TrimSpace(name[idx+len(" as "):])
				}
				refs = append(refs, ImportRef{Alias: alias, Path: path})
			}
		}

	case LangPython:
		for _, m := range rePyImport.FindAllStringSubmatch(src, -1) {
			path := m[1]
			alias := m[2]
			if alias == "" {
				alias = lastSegment(path, ".")
			}
			refs = append(refs, ImportRef{Alias: alias, Path: path})
		}
		for _, m := range rePyFromImport.FindAllStringSubmatch(src, -1) {
			path := m[1]
			for _, name := range strings.Split(m[2], ",") {
				name = strings.TrimSpace(strings.Trim(name, "()"))
				if name == "" || name == "*" {
					continue
				}
				alias := name
				if idx := strings.Index(name, " as "); idx >= 0 {
					alias = strings.TrimSpace(name[idx+len(" as "):])
					name = strings.TrimSpace(name[:idx])
				}
				// "from X import Y" brings Y into scope directly (bare
				// calls), so the resolvable path is X.Y, not just X.
				refs = append(refs, ImportRef{Alias: alias, Path: path + "." + name})
			}
		}
	}

	return refs
}

func lastSegment(path, sep string) string {
	parts := strings.Split(path, sep)
	return parts[len(parts)-1]
}
