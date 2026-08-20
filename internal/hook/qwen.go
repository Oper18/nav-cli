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
	Timeout int    `json:"timeout,omitempty"`
}

// InstallQwen writes the nav hook into Qwen Code settings.json.
// settingsPath is the full path to the settings.json file.
// project is the nav project name. topK is how many results to inject.
// timeoutSec bounds the hook (see InstallClaude's timeoutSec doc — same
// rationale applies here); <= 0 omits the field and Qwen Code's own default
// applies. Re-running InstallQwen when the hook is already present does not
// re-add it, but does sync its "timeout" field to the current timeoutSec —
// see InstallClaude's doc for why. installed reports whether settings.json
// was modified at all (a fresh add or a timeout sync).
func InstallQwen(settingsPath, project string, topK, timeoutSec int) (installed bool, err error) {
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return false, fmt.Errorf("creating settings directory: %w", err)
	}

	settings, err := readSettingsJSON(settingsPath)
	if err != nil {
		return false, err
	}

	// Navigate to hooks.UserPromptSubmit, creating as needed.
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
		settings["hooks"] = hooks
	}

	// Qwen Code, like Claude Code, never sets a $QWEN_USER_PROMPT env var —
	// the prompt only arrives as the "prompt" field of the JSON payload piped
	// to the hook on stdin, so the command extracts it with jq and hands it
	// to nav via --query-stdin (see InstallClaude for the same fix).
	navCommand := fmt.Sprintf(
		"jq -r '.prompt' | nav hook run %s --type qwen --top %d --query-stdin",
		project, topK,
	)

	// Check if already installed; if so, just sync its timeout field.
	existing, _ := hooks["UserPromptSubmit"].([]interface{})
	for _, raw := range existing {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if entryContainsNavQwen(entry) {
			if !syncHookTimeout(entry, "--type qwen", timeoutSec) {
				return false, nil // already installed, nothing to sync
			}
			if err := writeSettingsJSON(settingsPath, settings); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	// Build the new entry as a plain map so it round-trips cleanly.
	hookAction := map[string]interface{}{
		"type":    "command",
		"command": navCommand,
	}
	if timeoutSec > 0 {
		hookAction["timeout"] = timeoutSec
	}
	newEntry := map[string]interface{}{
		"matcher": "",
		"hooks":   []interface{}{hookAction},
	}

	hooks["UserPromptSubmit"] = append(existing, newEntry)

	if err := writeSettingsJSON(settingsPath, settings); err != nil {
		return false, err
	}
	return true, nil
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

// entryContainsNavQwen reports whether a raw hook entry map contains the nav
// Qwen command. It matches on "--type qwen" rather than the full "nav hook
// run --type qwen" prefix, because the actual command has the project name
// sitting between "run" and "--type" (see navCommand above) — matching the
// full prefix would never find the entry, making install non-idempotent.
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
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "--type qwen") {
			return true
		}
	}
	return false
}
