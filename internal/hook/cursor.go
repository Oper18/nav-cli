package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CursorHookEntry represents one hook entry in settings.json.
type CursorHookEntry struct {
	ID      string      `json:"id"`
	When    string      `json:"when"`
	Label   string      `json:"label"`
	Command string      `json:"command"`
	Props   interface{} `json:"props"`
}

// InstallCursor writes the nav hook into Cursor settings.
// settingsPath is the full path to the settings.json file.
// project is the nav project name. topK is how many results to inject.
func InstallCursor(settingsPath, project string, topK int) error {
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return fmt.Errorf("creating settings directory: %w", err)
	}

	settings, err := readSettingsJSON(settingsPath)
	if err != nil {
		return err
	}

	// Navigate to tools, creating as needed.
	tools, _ := settings["tools"].([]interface{})
	if tools == nil {
		tools = make([]interface{}, 0)
		settings["tools"] = tools
	}

	navCommand := fmt.Sprintf(
		"nav hook run %s --type cursor --top %d --query \"$SELECTED_TEXT\"",
		project, topK,
	)

	// Check if already installed.
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}
		if command, exists := toolMap["command"]; exists && strings.Contains(command.(string), navCommand) {
			return nil // already installed
		}
	}

	// Build the new entry as a plain map so it round-trips cleanly.
	newEntry := map[string]interface{}{
		"id":      "nav-cursor-hook",
		"when":    "true",
		"label":   "nav Context",
		"command": navCommand,
		"props": map[string]interface{}{
			"icon": "nav",
		},
	}

	settings["tools"] = append(tools, newEntry)

	return writeSettingsJSON(settingsPath, settings)
}

// UninstallCursor removes the nav hook from Cursor settings.
func UninstallCursor(settingsPath string) error {
	settings, err := readSettingsJSON(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	tools, ok := settings["tools"].([]interface{})
	if !ok {
		return nil
	}

	filtered := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			filtered = append(filtered, tool)
			continue
		}
		command, exists := toolMap["command"]
		if !exists || !strings.Contains(command.(string), "nav hook run --type cursor") {
			filtered = append(filtered, tool)
		}
	}

	settings["tools"] = filtered
	return writeSettingsJSON(settingsPath, settings)
}

// CursorDefaultSettingsPath returns the path to .cursor/settings.json in dir.
func CursorDefaultSettingsPath(dir string) string {
	return filepath.Join(dir, ".cursor", "settings.json")
}

// CursorGlobalSettingsPath returns ~/.cursor/settings.json.
func CursorGlobalSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cursor", "settings.json")
	}
	return filepath.Join(home, ".cursor", "settings.json")
}

// entryContainsNavCursor reports whether a raw hook entry map contains the nav Cursor command.
func entryContainsNavCursor(entry map[string]interface{}) bool {
	if command, exists := entry["command"]; exists {
		if cmdStr, ok := command.(string); ok {
			return strings.Contains(cmdStr, "nav hook run --type cursor")
		}
	}
	return false
}