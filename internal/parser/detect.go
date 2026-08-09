package parser

import (
	"path/filepath"
	"strings"

	"nav/config"
)

// Language constants
const (
	LangGo         = "go"
	LangPython     = "python"
	LangTypeScript = "typescript"
	LangJavaScript = "javascript"
	LangRust       = "rust"
	LangJava       = "java"
	LangC          = "c"
	LangCPP        = "cpp"
	LangRuby       = "ruby"
)

var extToLang = map[string]string{
	".go":   LangGo,
	".py":   LangPython,
	".ts":   LangTypeScript,
	".tsx":  LangTypeScript,
	".js":   LangJavaScript,
	".jsx":  LangJavaScript,
	".rs":   LangRust,
	".java": LangJava,
	".c":    LangC,
	".h":    LangC,
	".cpp":  LangCPP,
	".cc":   LangCPP,
	".cxx":  LangCPP,
	".hpp":  LangCPP,
	".rb":   LangRuby,
}

// DetectLanguage returns the language constant for a file path, or "" if unsupported.
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	return extToLang[ext]
}

// langToProgramming maps an internal language constant to the
// config.ProgrammingLanguage enum. Returns the empty enum value when there is
// no mapping.
var langToProgramming = map[string]config.ProgrammingLanguage{
	LangGo:         config.Go,
	LangPython:     config.Python,
	LangTypeScript: config.TS,
	LangJavaScript: config.JS,
}

// ProgrammingLanguage returns the typed config.ProgrammingLanguage for a file
// path, or "" if the language is not supported by the typed enum.
func ProgrammingLanguage(filePath string) config.ProgrammingLanguage {
	return langToProgramming[DetectLanguage(filePath)]
}

// ShouldSkip reports whether filePath matches any of the skip glob patterns.
// Also returns true if the base name (without extension) ends in "_test" for Go files.
//
// A "dir/**" (or "**/dir/**") pattern matches dir as a path component
// anywhere in filePath — mirroring .gitignore semantics — not just when dir
// sits at the very start of filePath. Without this, a pattern like
// "node_modules/**" only ever caught a top-level node_modules/, silently
// letting nested ones (e.g. some-tool/node_modules/, vendored deep in a
// subdirectory) through to be indexed in full.
func ShouldSkip(filePath string, patterns []string) bool {
	normalizedPath := filepath.ToSlash(filePath)
	segments := strings.Split(normalizedPath, "/")
	base := segments[len(segments)-1]

	for _, pattern := range patterns {
		normalizedPattern := filepath.ToSlash(pattern)

		switch {
		case strings.HasSuffix(normalizedPattern, "/**"):
			// "dir/**" or "**/dir/**" — dir as a path component anywhere.
			dir := strings.TrimSuffix(normalizedPattern, "/**")
			dir = strings.TrimPrefix(dir, "**/")
			if dir != "" && pathContainsDir(segments[:len(segments)-1], dir) {
				return true
			}
		case strings.HasPrefix(normalizedPattern, "**/"):
			// "**/*.ext" — filename pattern matched at any depth, not just
			// directly under the repo root.
			namePattern := strings.TrimPrefix(normalizedPattern, "**/")
			if matched, err := filepath.Match(namePattern, base); err == nil && matched {
				return true
			}
		default:
			// Standard glob matching, against the full path and the base name.
			if matched, err := filepath.Match(normalizedPattern, normalizedPath); err == nil && matched {
				return true
			}
			if matched, err := filepath.Match(normalizedPattern, base); err == nil && matched {
				return true
			}
		}
	}

	// For Go files, skip test files (base name without extension ends in "_test").
	if DetectLanguage(filePath) == LangGo {
		nameWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))
		if strings.HasSuffix(nameWithoutExt, "_test") {
			return true
		}
	}

	return false
}

// pathContainsDir reports whether dir (a possibly multi-segment,
// slash-separated relative path) appears as a contiguous run of dirSegments
// anywhere within dirSegments — i.e. filePath passes through dir at some
// depth, not necessarily starting at the repo root.
func pathContainsDir(dirSegments []string, dir string) bool {
	needle := strings.Split(dir, "/")
	if len(needle) > len(dirSegments) {
		return false
	}
	for i := 0; i+len(needle) <= len(dirSegments); i++ {
		match := true
		for j, seg := range needle {
			if dirSegments[i+j] != seg {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// InferLayer returns a layer label from the file path heuristic.
// Looks for known path segments and returns a canonical label.
func InferLayer(filePath string) string {
	// Normalise separators and split into segments.
	normalised := filepath.ToSlash(filePath)
	segments := strings.Split(normalised, "/")

	for _, seg := range segments {
		lower := strings.ToLower(seg)
		// Strip extension from the last segment when comparing.
		lower = strings.TrimSuffix(lower, filepath.Ext(lower))

		switch lower {
		case "controller", "controllers":
			return "controller"
		case "service", "services":
			return "service"
		case "repository", "repositories", "repo", "repos":
			return "repository"
		case "model", "models":
			return "model"
		case "middleware", "middlewares":
			return "middleware"
		case "handler", "handlers":
			return "handler"
		case "util", "utils", "helper", "helpers":
			return "util"
		}
	}

	return ""
}

// InferModule converts a file path to a dotted module path.
// e.g. "services/user/service.py" → "services.user.service"
// Strips the file extension and replaces "/" with ".".
func InferModule(filePath string) string {
	// Normalise to forward slashes.
	normalised := filepath.ToSlash(filePath)
	// Strip extension.
	ext := filepath.Ext(normalised)
	if ext != "" {
		normalised = strings.TrimSuffix(normalised, ext)
	}
	// Replace path separators with dots.
	return strings.ReplaceAll(normalised, "/", ".")
}
