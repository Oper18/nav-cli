package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cursorSessionStartMarker identifies nav's entry under hooks.sessionStart
// in hooks.json, for install idempotency and uninstall.
const cursorSessionStartMarker = "--type cursor-session-start"

// InstallCursor writes nav's sessionStart hook into Cursor's hooks.json.
// hooksPath is the full path to hooks.json (project: .cursor/hooks.json,
// global: ~/.cursor/hooks.json — see CursorDefaultSettingsPath and
// CursorGlobalSettingsPath). project is the nav project name. It returns
// installed=false when the hook was already present, leaving hooks.json
// untouched in that case.
//
// This targets sessionStart rather than a per-prompt hook because Cursor's
// hook API (https://cursor.com/docs/hooks) has no event that can inject
// context into a specific prompt: beforeSubmitPrompt is the only per-prompt
// hook, and its response schema is {continue, user_message} only — it can
// block a prompt but not add context to it (an open Cursor feature request
// as of 2026). sessionStart's response schema does support additional
// context, so nav injects its cached knowledge-graph digest there instead —
// the same digest Claude Code's SessionStart hook prints (see
// runHookRunClaudeSessionStart) — once per session rather than per prompt.
func InstallCursor(hooksPath, project string) (installed bool, err error) {
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		return false, fmt.Errorf("creating hooks directory: %w", err)
	}

	settings, err := readSettingsJSON(hooksPath)
	if err != nil {
		return false, err
	}

	if _, ok := settings["version"]; !ok {
		settings["version"] = 1
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
		settings["hooks"] = hooks
	}

	command := fmt.Sprintf("nav hook run %s --type cursor-session-start", project)

	existing, _ := hooks["sessionStart"].([]interface{})
	for _, raw := range existing {
		if entry, ok := raw.(map[string]interface{}); ok && cursorEntryContainsCommand(entry, cursorSessionStartMarker) {
			return false, nil // already installed
		}
	}

	newEntry := map[string]interface{}{"command": command}
	hooks["sessionStart"] = append(existing, newEntry)

	if err := writeSettingsJSON(hooksPath, settings); err != nil {
		return false, err
	}
	return true, nil
}

// UninstallCursor removes the nav sessionStart hook from Cursor's hooks.json.
func UninstallCursor(hooksPath string) error {
	settings, err := readSettingsJSON(hooksPath)
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

	existing, ok := hooks["sessionStart"].([]interface{})
	if !ok {
		return nil
	}

	filtered := make([]interface{}, 0, len(existing))
	for _, raw := range existing {
		entry, ok := raw.(map[string]interface{})
		if !ok || !cursorEntryContainsCommand(entry, cursorSessionStartMarker) {
			filtered = append(filtered, raw)
		}
	}

	hooks["sessionStart"] = filtered
	return writeSettingsJSON(hooksPath, settings)
}

// CursorDefaultSettingsPath returns the path to .cursor/hooks.json in dir.
func CursorDefaultSettingsPath(dir string) string {
	return filepath.Join(dir, ".cursor", "hooks.json")
}

// CursorGlobalSettingsPath returns ~/.cursor/hooks.json.
func CursorGlobalSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cursor", "hooks.json")
	}
	return filepath.Join(home, ".cursor", "hooks.json")
}

// cursorEntryContainsCommand reports whether a flat Cursor hook entry
// ({"command": "..."}) contains marker in its command. Unlike Claude/Qwen,
// Cursor hook entries have no nested "hooks" array — each entry under
// hooks.<event> is just {"command": "..."} directly.
func cursorEntryContainsCommand(entry map[string]interface{}, marker string) bool {
	cmd, _ := entry["command"].(string)
	return strings.Contains(cmd, marker)
}
