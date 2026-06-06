package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// QwenHookEntry represents one hook entry in settings.json.
type QwenHookEntry struct {
	Matcher string     `json:"matcher"`
	Hooks   []QwenHook `json:"hooks"`
}

// QwenHook represents a single hook action inside a QwenHookEntry.
type QwenHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// InstallQwen writes the nav hook into Qwen Code settings.json.
// settingsPath is the full path to the settings.json file.
// project is the nav project name. topK is how many results to inject.
func InstallQwen(settingsPath, project string, topK int) error {
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
		"nav hook run %s --type qwen --top %d --query \"$QWEN_USER_PROMPT\"",
		project, topK,
	)

	// Check if already installed.
	existing, _ := hooks["UserPromptSubmit"].([]interface{})
	for _, raw := range existing {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if entryContainsNavQwen(entry) {
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

// UninstallQwen removes the nav hook from Qwen Code settings.json.
func UninstallQwen(settingsPath string) error {
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
		if !ok || !entryContainsNavQwen(entry) {
			filtered = append(filtered, raw)
		}
	}

	hooks["UserPromptSubmit"] = filtered
	return writeSettingsJSON(settingsPath, settings)
}

// QwenDefaultSettingsPath returns the path to .qwen/settings.json in dir if it exists;
// otherwise assumes the directory is a project root and returns <dir>/.qwenrc.json.
func QwenDefaultSettingsPath(dir string) string {
	subdirPath := filepath.Join(dir, ".qwen", "settings.json")
	if _, err := os.Stat(subdirPath); err == nil {
		return subdirPath
	}
	
	// Fallback: check for Qwen configuration in home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".qwen", "settings.json")
	}
	return filepath.Join(home, ".qwen", "settings.json")
}

// QwenGlobalSettingsPath returns ~/.qwen/settings.json.
func QwenGlobalSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".qwen", "settings.json")
	}
	return filepath.Join(home, ".qwen", "settings.json")
}

// entryContainsNavQwen reports whether a raw hook entry map contains the nav Qwen command.
func entryContainsNavQwen(entry map[string]interface{}) bool {
	hookList, ok := entry["hooks"].([]interface{})
	if !ok {
		return false
	}
	for _, h := range hookList {
		hm, ok := h.(map[string]interface{})
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "nav hook run --type qwen") {
			return true
		}
	}
	return false
}