package parser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nav/internal/db/qdrant"
)

const minLines = 3

// Symbol holds fully resolved metadata for one code symbol.
type Symbol struct {
	qdrant.Payload
}

// callKeywords are identifiers that should not be treated as function calls.
var callKeywords = map[string]bool{
	"if":        true,
	"for":       true,
	"switch":    true,
	"select":    true,
	"func":      true,
	"make":      true,
	"append":    true,
	"len":       true,
	"cap":       true,
	"new":       true,
	"delete":    true,
	"close":     true,
	"panic":     true,
	"recover":   true,
	"print":     true,
	"println":   true,
}

// reCall matches qualified or bare function calls: word.word( or word(
var reCall = regexp.MustCompile(`\b(\w+(?:\.\w+)?)\s*\(`)

// ExtractCallsFromSource extracts function call names from source text.
// Returns a deduplicated list of qualified calls like "obj.Method" and bare calls like "funcName".
func ExtractCallsFromSource(source string) []string {
	matches := reCall.FindAllStringSubmatch(source, -1)
	seen := make(map[string]bool)
	var result []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		call := m[1]
		// For bare calls, check against the keyword list.
		bare := call
		if idx := strings.LastIndex(call, "."); idx >= 0 {
			bare = call[idx+1:]
		}
		if callKeywords[bare] || callKeywords[call] {
			continue
		}
		if !seen[call] {
			seen[call] = true
			result = append(result, call)
		}
	}
	return result
}

// reImportPath matches a single import path string (with or without an alias).
// e.g.   "fmt"  or   alias "some/pkg/name"
var reImportPath = regexp.MustCompile(`(?m)^\s*(?:\w+\s+)?"([^"]+)"`)

// extractImports scans full file source for import paths and returns the last
// path segment (i.e. the identifier that code would normally reference) for
// each path that appears anywhere in symbolContent.
func extractImports(fullSource, symbolContent string) []string {
	// Find the import block(s).
	matches := reImportPath.FindAllStringSubmatch(fullSource, -1)
	seen := make(map[string]bool)
	var result []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		importPath := m[1]
		// Derive the identifier from the last path segment.
		parts := strings.Split(importPath, "/")
		ident := parts[len(parts)-1]
		// Check if this identifier appears in the symbol content.
		if strings.Contains(symbolContent, ident) && !seen[ident] {
			seen[ident] = true
			result = append(result, ident)
		}
	}
	return result
}

// buildSignature constructs a human-readable signature string.
func buildSignature(raw RawSymbol) string {
	var b strings.Builder
	if raw.Receiver != "" {
		b.WriteString("(")
		b.WriteString(raw.Receiver)
		b.WriteString(") ")
	}
	b.WriteString(raw.Name)
	if raw.Params != "" {
		b.WriteString(raw.Params)
	}
	if raw.Result != "" {
		b.WriteString(" ")
		b.WriteString(raw.Result)
	}
	return b.String()
}

// gitLastModified returns the Unix timestamp of the last git commit that touched filePath.
// repoRoot is used as the working directory. Returns current time on failure.
func gitLastModified(repoRoot, filePath string) int64 {
	cmd := exec.Command("git", "log", "-1", "--format=%ct", "--", filePath)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return time.Now().Unix()
	}
	ts := strings.TrimSpace(string(out))
	if ts == "" {
		return time.Now().Unix()
	}
	v, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Now().Unix()
	}
	return v
}

// ExtractSymbols parses filePath (relative to repoRoot) and returns all symbols.
// branch is the git branch the symbols belong to and gets stored in each
// Payload alongside the symbol.
func ExtractSymbols(ctx context.Context, repoRoot, filePath, branch string) ([]Symbol, error) {
	absPath := filepath.Join(repoRoot, filePath)
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", absPath, err)
	}

	lang := DetectLanguage(filePath)
	if lang == "" {
		return nil, nil // unsupported language — not an error
	}

	rawSymbols, err := ParseFile(ctx, lang, src)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	lastMod := gitLastModified(repoRoot, filePath)
	progLang := ProgrammingLanguage(filePath)

	var symbols []Symbol
	for _, raw := range rawSymbols {
		// Skip symbols with too few lines of content.
		lineCount := strings.Count(raw.Content, "\n") + 1
		if lineCount < minLines {
			continue
		}

		qualifiedName := raw.Name
		if raw.Receiver != "" {
			qualifiedName = raw.Receiver + "." + raw.Name
		}

		calls := ExtractCallsFromSource(raw.Content)

		sym := Symbol{
			Payload: qdrant.Payload{
				Symbol:       qualifiedName,
				FilePath:     filePath,
				Content:      raw.Content,
				Language:     progLang,
				Type:         raw.Type,
				Calls:        calls,
				LastModified: lastMod,
				Branch:       branch,
			},
		}
		symbols = append(symbols, sym)
	}

	return symbols, nil
}
