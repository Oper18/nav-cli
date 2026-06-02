package parser

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// RawSymbol holds the raw data extracted by tree-sitter before enrichment.
type RawSymbol struct {
	Type      string // "function", "method", "class", "struct", "interface", "enum", "trait"
	Name      string // bare symbol name
	Receiver  string // for methods: receiver type name (e.g. "UserService")
	Params    string // raw parameter list text
	Result    string // raw return type text
	Body      string // raw body text
	Content   string // full source text of the node
	StartLine uint32
	EndLine   uint32
}

// getLanguage returns the tree-sitter Language for the given language constant.
func getLanguage(lang string) (*sitter.Language, error) {
	switch lang {
	case LangGo:
		return golang.GetLanguage(), nil
	case LangPython:
		return python.GetLanguage(), nil
	case LangTypeScript:
		return typescript.GetLanguage(), nil
	case LangJavaScript:
		return javascript.GetLanguage(), nil
	case LangRust:
		return rust.GetLanguage(), nil
	case LangJava:
		return java.GetLanguage(), nil
	case LangRuby:
		return ruby.GetLanguage(), nil
	// TODO: add C/C++ grammar
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// captureIndexByName builds a map from capture name to its index in a query.
func captureIndexByName(q *sitter.Query) map[string]uint32 {
	m := make(map[string]uint32)
	for i := uint32(0); i < q.CaptureCount(); i++ {
		m[q.CaptureNameForId(i)] = i
	}
	return m
}

// classNodeTypes lists the tree-sitter node types that represent class-like
// scopes whose name should become the receiver of any function defined inside.
var classNodeTypes = map[string]map[string]bool{
	LangPython:     {"class_definition": true},
	LangJavaScript: {"class_declaration": true},
	LangTypeScript: {"class_declaration": true},
	LangJava:       {"class_declaration": true, "interface_declaration": true},
	LangRuby:       {"class": true, "module": true},
}

// enclosingClassName walks up node's parents and returns the name of the
// closest enclosing class-like declaration. Returns "" when there is no
// enclosing class or the language has no class concept tracked here.
func enclosingClassName(node *sitter.Node, sourceCode []byte, lang string) string {
	types := classNodeTypes[lang]
	if types == nil {
		return ""
	}
	parent := node.Parent()
	for parent != nil {
		if types[parent.Type()] {
			if name := parent.ChildByFieldName("name"); name != nil {
				return name.Content(sourceCode)
			}
		}
		parent = parent.Parent()
	}
	return ""
}

// extractReceiver strips leading punctuation and returns just the type identifier
// from a receiver parameter list text like "(*UserService)".
func extractReceiver(raw string) string {
	// Remove surrounding parens.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "(")
	raw = strings.TrimSuffix(raw, ")")
	raw = strings.TrimSpace(raw)

	// A receiver looks like "u *UserService" or "u UserService".
	// We want only the type name.
	parts := strings.Fields(raw)
	typePart := ""
	if len(parts) == 1 {
		typePart = parts[0]
	} else if len(parts) >= 2 {
		// Last part is usually the type (possibly with * prefix).
		typePart = parts[len(parts)-1]
	}

	// Strip leading '*'.
	typePart = strings.TrimLeft(typePart, "*")
	return typePart
}

// ParseFile parses sourceCode with the appropriate tree-sitter grammar for lang
// and returns all raw symbols found.
// Returns an error if the language is unsupported or parsing fails.
func ParseFile(ctx context.Context, lang string, sourceCode []byte) ([]RawSymbol, error) {
	langGrammar, err := getLanguage(lang)
	if err != nil {
		return nil, err
	}

	queryStr := QueryForLanguage(lang)
	if queryStr == "" {
		return nil, fmt.Errorf("no query defined for language: %s", lang)
	}

	p := sitter.NewParser()
	p.SetLanguage(langGrammar)

	tree, err := p.ParseCtx(ctx, nil, sourceCode)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	defer tree.Close()

	q, err := sitter.NewQuery([]byte(queryStr), langGrammar)
	if err != nil {
		return nil, fmt.Errorf("query compile error: %w", err)
	}
	defer q.Close()

	captureNames := captureIndexByName(q)

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, tree.RootNode())

	// Track which definition nodes we have already recorded (by their byte offset)
	// to avoid duplicates when a pattern matches the same node multiple times.
	seen := make(map[uint32]bool)

	var symbols []RawSymbol

	for {
		match, ok := qc.NextMatch()
		if !ok {
			break
		}

		// Organise captures by name for this match.
		caps := make(map[string]*sitter.Node)
		for _, cap := range match.Captures {
			name := q.CaptureNameForId(cap.Index)
			caps[name] = cap.Node
		}

		// Determine which pattern fired based on which top-level anchor is present.
		_ = captureNames // used indirectly via caps

		var defNode *sitter.Node
		symType := ""

		switch {
		case caps["definition"] != nil && caps["struct_definition"] == nil && caps["class_definition"] == nil &&
			caps["interface_definition"] == nil && caps["enum_definition"] == nil && caps["trait_definition"] == nil:
			defNode = caps["definition"]
			// function or method — determined later.
			symType = "function"
		case caps["struct_definition"] != nil:
			defNode = caps["struct_definition"]
			symType = "struct"
		case caps["class_definition"] != nil:
			defNode = caps["class_definition"]
			symType = "class"
		case caps["interface_definition"] != nil:
			defNode = caps["interface_definition"]
			symType = "interface"
		case caps["enum_definition"] != nil:
			defNode = caps["enum_definition"]
			symType = "enum"
		case caps["trait_definition"] != nil:
			defNode = caps["trait_definition"]
			symType = "trait"
		default:
			// Pattern has @definition nested inside impl_item — the @definition
			// capture for the inner function_item fires here.
			if caps["definition"] != nil {
				defNode = caps["definition"]
				symType = "function"
			} else {
				continue
			}
		}

		if defNode == nil {
			continue
		}

		// Deduplicate by start byte.
		startByte := defNode.StartByte()
		if seen[startByte] {
			continue
		}
		seen[startByte] = true

		nameNode := caps["name"]
		if nameNode == nil {
			continue
		}

		sym := RawSymbol{
			Type:      symType,
			Name:      nameNode.Content(sourceCode),
			StartLine: defNode.StartPoint().Row,
			EndLine:   defNode.EndPoint().Row,
			Content:   defNode.Content(sourceCode),
		}

		if n := caps["receiver"]; n != nil {
			sym.Receiver = extractReceiver(n.Content(sourceCode))
			sym.Type = "method"
		} else if symType == "function" {
			// Languages without an explicit receiver capture (Python, JS/TS, ...)
			// — walk up the AST to find an enclosing class declaration.
			if cls := enclosingClassName(defNode, sourceCode, lang); cls != "" {
				sym.Receiver = cls
				sym.Type = "method"
			}
		}
		if n := caps["params"]; n != nil {
			sym.Params = n.Content(sourceCode)
		}
		if n := caps["result"]; n != nil {
			sym.Result = n.Content(sourceCode)
		}
		if n := caps["body"]; n != nil {
			sym.Body = n.Content(sourceCode)
		}

		symbols = append(symbols, sym)
	}

	return symbols, nil
}
