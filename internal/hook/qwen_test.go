package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readQwenPromptTimeout(t *testing.T, settingsPath string) (timeout int, present bool) {
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
	entries, _ := hooks["UserPromptSubmit"].([]interface{})
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
			if !entryContainsNavQwen(entry) {
				continue
			}
			n, ok := hm["timeout"]
			if !ok {
				return 0, false
			}
			f, _ := n.(float64)
			return int(f), true
		}
	}
	t.Fatalf("no nav Qwen UserPromptSubmit entry found in %s", settingsPath)
	return 0, false
}

func TestInstallQwenWritesTimeout(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	installed, err := InstallQwen(settingsPath, "myproject", 5, 45)
	if err != nil {
		t.Fatalf("InstallQwen: %v", err)
	}
	if !installed {
		t.Error("InstallQwen on an empty settings file should report installed = true")
	}
	if timeout, present := readQwenPromptTimeout(t, settingsPath); !present || timeout != 45 {
		t.Errorf("timeout = %v (present=%v), want 45", timeout, present)
	}
}

func TestInstallQwenSyncsTimeoutOnReinstall(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallQwen(settingsPath, "myproject", 5, 30); err != nil {
		t.Fatalf("InstallQwen (initial): %v", err)
	}

	// Same timeout: re-running should be a true no-op.
	if installed, err := InstallQwen(settingsPath, "myproject", 5, 30); err != nil {
		t.Fatalf("InstallQwen (same timeout): %v", err)
	} else if installed {
		t.Error("re-running InstallQwen with an unchanged timeout should report installed = false")
	}

	// Raised timeout: re-running should sync the field in place, not add a
	// second entry.
	installed, err := InstallQwen(settingsPath, "myproject", 5, 90)
	if err != nil {
		t.Fatalf("InstallQwen (raised timeout): %v", err)
	}
	if !installed {
		t.Error("re-running InstallQwen with a changed timeout should report installed = true")
	}
	if timeout, present := readQwenPromptTimeout(t, settingsPath); !present || timeout != 90 {
		t.Errorf("timeout after resync = %v (present=%v), want 90", timeout, present)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings: %v", err)
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	entries, _ := hooks["UserPromptSubmit"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("UserPromptSubmit entries = %d, want exactly 1 (resync should not duplicate)", len(entries))
	}
}

func TestUninstallQwenRemovesHook(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallQwen(settingsPath, "myproject", 5, 45); err != nil {
		t.Fatalf("InstallQwen: %v", err)
	}
	if err := UninstallQwen(settingsPath); err != nil {
		t.Fatalf("UninstallQwen: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings: %v", err)
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	entries, _ := hooks["UserPromptSubmit"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("UserPromptSubmit entries after uninstall = %d, want 0", len(entries))
	}
}
