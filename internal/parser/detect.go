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
func ShouldSkip(filePath string, patterns []string) bool {
	for _, pattern := range patterns {
		// Handle the special case of "directory/**" pattern which should match
		// all nested paths within that directory
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			// Convert relative path separators consistently for comparison
			normalizedPath := filepath.ToSlash(filePath)
			normalizedPrefix := filepath.ToSlash(prefix)
			
			// Check if the file path starts with the directory prefix
			if normalizedPath == normalizedPrefix || strings.HasPrefix(normalizedPath+"/", normalizedPrefix+"/") {
				return true
			}
		} else {
			// Standard glob matching
			matched, err := filepath.Match(pattern, filePath)
			if err == nil && matched {
				return true
			}
			// Also match against just the base name.
			matched, err = filepath.Match(pattern, filepath.Base(filePath))
			if err == nil && matched {
				return true
			}
		}
	}

	// For Go files, skip test files (base name without extension ends in "_test").
	if DetectLanguage(filePath) == LangGo {
		base := filepath.Base(filePath)
		nameWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))
		if strings.HasSuffix(nameWithoutExt, "_test") {
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
