package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	installed, err := InstallClaude(settingsPath, "myproject", 5)
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

func TestInstallClaudeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	for i := 0; i < 3; i++ {
		installed, err := InstallClaude(settingsPath, "myproject", 5)
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

	if _, err := InstallClaude(settingsPath, "myproject", 5); err != nil {
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
