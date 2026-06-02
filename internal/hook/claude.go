package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeHookEntry represents one hook entry in settings.json.
type ClaudeHookEntry struct {
	Matcher string      `json:"matcher"`
	Hooks   []ClaudeHook `json:"hooks"`
}

// ClaudeHook represents a single hook action inside a ClaudeHookEntry.
type ClaudeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// ContextResult holds a single search result to be formatted into context output.
type ContextResult struct {
	Score   float32
	Symbol  string
	Type    string
	File    string
	Layer   string
	Purpose string
	Code    string
}

// InstallClaude writes the nav hook into Claude Code settings.json.
// settingsPath is the full path to the settings.json file.
// project is the nav project name. topK is how many results to inject.
func InstallClaude(settingsPath, project string, topK int) error {
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return fmt.Errorf("creating settings directory: %w", err)
	}

	settings, err := readSettingsJSON(settingsPath)
	if err != nil {
		return err
	}

	// Navigate to hooks.UserPromptSubmit, creating as needed.
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
		settings["hooks"] = hooks
	}

	navCommand := fmt.Sprintf(
		"nav hook run %s --type claude --top %d --query \"$CLAUDE_USER_PROMPT\"",
		project, topK,
	)

	// Check if already installed.
	existing, _ := hooks["UserPromptSubmit"].([]interface{})
	for _, raw := range existing {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if entryContainsNavClaude(entry) {
			return nil // already installed
		}
	}

	// Build the new entry as a plain map so it round-trips cleanly.
	newEntry := map[string]interface{}{
		"matcher": "",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": navCommand,
			},
		},
	}

	hooks["UserPromptSubmit"] = append(existing, newEntry)

	return writeSettingsJSON(settingsPath, settings)
}

// UninstallClaude removes the nav hook from Claude Code settings.json.
func UninstallClaude(settingsPath string) error {
	settings, err := readSettingsJSON(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return nil
	}

	existing, ok := hooks["UserPromptSubmit"].([]interface{})
	if !ok {
		return nil
	}

	filtered := make([]interface{}, 0, len(existing))
	for _, raw := range existing {
		entry, ok := raw.(map[string]interface{})
		if !ok || !entryContainsNavClaude(entry) {
			filtered = append(filtered, raw)
		}
	}

	hooks["UserPromptSubmit"] = filtered
	return writeSettingsJSON(settingsPath, settings)
}

// DefaultSettingsPath returns the path to .claude/settings.json in dir.
func DefaultSettingsPath(dir string) string {
	return filepath.Join(dir, ".claude", "settings.json")
}

// GlobalSettingsPath returns ~/.claude/settings.json.
func GlobalSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "settings.json")
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// FormatContextBlock formats search results as a <nav-context> XML block for injection
// into a Claude Code session. Output is truncated to approximately maxTokens characters
// (rough heuristic: 1 token ≈ 4 chars).
func FormatContextBlock(project, query string, results []ContextResult, maxTokens int) string {
	maxChars := maxTokens * 4

	var sb strings.Builder
	fmt.Fprintf(&sb, "<nav-context project=%q query=%q>\n", project, query)
	header := sb.String()

	footer := "</nav-context>"

	var body strings.Builder
	for i, r := range results {
		var section strings.Builder
		fmt.Fprintf(&section, "--- Result %d (score: %.2f) ---\n", i+1, r.Score)
		fmt.Fprintf(&section, "Symbol: %s\n", r.Symbol)
		fmt.Fprintf(&section, "Type:   %s\n", r.Type)
		fmt.Fprintf(&section, "File:   %s\n", r.File)
		if r.Layer != "" {
			fmt.Fprintf(&section, "Layer:  %s\n", r.Layer)
		}
		if r.Purpose != "" {
			fmt.Fprintf(&section, "\nPurpose:\n%s\n", r.Purpose)
		}
		if r.Code != "" {
			fmt.Fprintf(&section, "\nCode:\n%s\n", r.Code)
		}
		section.WriteString("\n")

		candidate := body.String() + section.String()
		if len(header)+len(candidate)+len(footer) > maxChars && body.Len() > 0 {
			// Adding this result would exceed the budget — stop here.
			break
		}
		body.WriteString(section.String())
	}

	return header + body.String() + footer
}

// --- helpers ---

// readSettingsJSON reads and JSON-parses a settings file, returning an empty map
// when the file does not exist.
func readSettingsJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// writeSettingsJSON serialises settings back to disk with 2-space indentation.
func writeSettingsJSON(path string, settings map[string]interface{}) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// entryContainsNavClaude reports whether a raw hook entry map contains the nav Claude command.
func entryContainsNavClaude(entry map[string]interface{}) bool {
	hookList, ok := entry["hooks"].([]interface{})
	if !ok {
		return false
	}
	for _, h := range hookList {
		hm, ok := h.(map[string]interface{})
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "nav hook run --type claude") {
			return true
		}
	}
	return false
}
