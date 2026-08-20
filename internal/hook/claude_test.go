package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readHookCommands(t *testing.T, settingsPath, event string) []string {
	t.Helper()
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings: %v", err)
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	entries, _ := hooks[event].([]interface{})

	var cmds []string
	for _, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		hookList, _ := entry["hooks"].([]interface{})
		for _, h := range hookList {
			hm, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, ok := hm["command"].(string); ok {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

func TestInstallClaudeRegistersBothHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	installed, err := InstallClaude(settingsPath, "myproject", 5, 45)
	if err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	if !installed {
		t.Error("InstallClaude on an empty settings file should report installed = true")
	}

	prompts := readHookCommands(t, settingsPath, "UserPromptSubmit")
	if len(prompts) != 1 {
		t.Fatalf("UserPromptSubmit entries = %v, want exactly 1", prompts)
	}

	starts := readHookCommands(t, settingsPath, "SessionStart")
	if len(starts) != 1 {
		t.Fatalf("SessionStart entries = %v, want exactly 1", starts)
	}
	if starts[0] != `nav hook run myproject --type claude-session-start` {
		t.Errorf("SessionStart command = %q", starts[0])
	}
}

// readHookTimeouts returns the "timeout" field (0 if absent) of every hook
// action under hooks.<event>, in the same order as readHookCommands.
func readHookTimeouts(t *testing.T, settingsPath, event string) []int {
	t.Helper()
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings: %v", err)
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	entries, _ := hooks[event].([]interface{})

	var timeouts []int
	for _, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		hookList, _ := entry["hooks"].([]interface{})
		for _, h := range hookList {
			hm, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			timeout, _ := hm["timeout"].(float64) // json numbers decode as float64
			timeouts = append(timeouts, int(timeout))
		}
	}
	return timeouts
}

func TestInstallClaudeWritesPromptTimeoutOnly(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallClaude(settingsPath, "myproject", 5, 45); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}

	// UserPromptSubmit can run a lazy sync + embed, so it gets the
	// configured timeout raised above Claude Code's own 30s default.
	if timeouts := readHookTimeouts(t, settingsPath, "UserPromptSubmit"); len(timeouts) != 1 || timeouts[0] != 45 {
		t.Errorf("UserPromptSubmit timeouts = %v, want [45]", timeouts)
	}
	// SessionStart only prints an already-cached digest, so it carries no
	// timeout override.
	if timeouts := readHookTimeouts(t, settingsPath, "SessionStart"); len(timeouts) != 1 || timeouts[0] != 0 {
		t.Errorf("SessionStart timeouts = %v, want [0] (no override)", timeouts)
	}
}

func TestInstallClaudeOmitsTimeoutFieldWhenNonPositive(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallClaude(settingsPath, "myproject", 5, 0); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	if strings.Contains(string(data), `"timeout"`) {
		t.Errorf("settings.json should have no \"timeout\" field when timeoutSec <= 0, got:\n%s", data)
	}
}

func TestInstallClaudeSyncsPromptTimeoutOnReinstall(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallClaude(settingsPath, "myproject", 5, 30); err != nil {
		t.Fatalf("InstallClaude (initial): %v", err)
	}

	// Same timeout: re-running should be a true no-op.
	if installed, err := InstallClaude(settingsPath, "myproject", 5, 30); err != nil {
		t.Fatalf("InstallClaude (same timeout): %v", err)
	} else if installed {
		t.Error("re-running InstallClaude with an unchanged timeout should report installed = false")
	}

	// Raised timeout: re-running should sync the field in place, not add a
	// second entry.
	installed, err := InstallClaude(settingsPath, "myproject", 5, 90)
	if err != nil {
		t.Fatalf("InstallClaude (raised timeout): %v", err)
	}
	if !installed {
		t.Error("re-running InstallClaude with a changed timeout should report installed = true")
	}
	if timeouts := readHookTimeouts(t, settingsPath, "UserPromptSubmit"); len(timeouts) != 1 || timeouts[0] != 90 {
		t.Errorf("UserPromptSubmit timeouts after resync = %v, want [90]", timeouts)
	}
	if prompts := readHookCommands(t, settingsPath, "UserPromptSubmit"); len(prompts) != 1 {
		t.Fatalf("UserPromptSubmit entries after resync = %v, want exactly 1 (resync should not duplicate)", prompts)
	}
}

func TestInstallClaudeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	for i := 0; i < 3; i++ {
		installed, err := InstallClaude(settingsPath, "myproject", 5, 45)
		if err != nil {
			t.Fatalf("InstallClaude run %d: %v", i, err)
		}
		wantInstalled := i == 0
		if installed != wantInstalled {
			t.Errorf("InstallClaude run %d: installed = %v, want %v", i, installed, wantInstalled)
		}
	}

	prompts := readHookCommands(t, settingsPath, "UserPromptSubmit")
	if len(prompts) != 1 {
		t.Fatalf("UserPromptSubmit entries after 3 installs = %v, want exactly 1 (idempotency regressed)", prompts)
	}
	starts := readHookCommands(t, settingsPath, "SessionStart")
	if len(starts) != 1 {
		t.Fatalf("SessionStart entries after 3 installs = %v, want exactly 1 (idempotency regressed)", starts)
	}
}

func TestUninstallClaudeRemovesBothHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallClaude(settingsPath, "myproject", 5, 45); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	if err := UninstallClaude(settingsPath); err != nil {
		t.Fatalf("UninstallClaude: %v", err)
	}

	if prompts := readHookCommands(t, settingsPath, "UserPromptSubmit"); len(prompts) != 0 {
		t.Errorf("UserPromptSubmit entries after uninstall = %v, want none", prompts)
	}
	if starts := readHookCommands(t, settingsPath, "SessionStart"); len(starts) != 0 {
		t.Errorf("SessionStart entries after uninstall = %v, want none", starts)
	}
}
