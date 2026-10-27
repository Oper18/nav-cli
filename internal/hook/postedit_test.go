package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallQwenRegistersPostEditSyncAndUninstalls(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if _, err := InstallQwen(settingsPath, "proj", 5, 30); err != nil {
		t.Fatalf("InstallQwen: %v", err)
	}
	post := readHookCommands(t, settingsPath, "PostToolUse")
	if len(post) != 1 || post[0] != "nav hook run proj --type qwen-post-edit" {
		t.Fatalf("PostToolUse = %v", post)
	}
	if installed, _ := InstallQwen(settingsPath, "proj", 5, 30); installed {
		t.Error("second install should be a no-op")
	}
	if err := UninstallQwen(settingsPath); err != nil {
		t.Fatalf("UninstallQwen: %v", err)
	}
	if left := readHookCommands(t, settingsPath, "PostToolUse"); len(left) != 0 {
		t.Errorf("PostToolUse after uninstall = %v", left)
	}
}

func TestInstallCursorRegistersAfterFileEditAndUninstalls(t *testing.T) {
	hooksPath := filepath.Join(t.TempDir(), "hooks.json")
	if installed, err := InstallCursor(hooksPath, "proj"); err != nil || !installed {
		t.Fatalf("InstallCursor = %v, %v", installed, err)
	}
	data, _ := os.ReadFile(hooksPath)
	if !strings.Contains(string(data), "nav hook run proj --type cursor-post-edit") || !strings.Contains(string(data), "nav hook run proj --type cursor-pre-prompt") {
		t.Fatalf("afterFileEdit missing in %s", data)
	}
	if installed, _ := InstallCursor(hooksPath, "proj"); installed {
		t.Error("second install should be a no-op")
	}
	if err := UninstallCursor(hooksPath); err != nil {
		t.Fatalf("UninstallCursor: %v", err)
	}
	data, _ = os.ReadFile(hooksPath)
	if strings.Contains(string(data), "cursor-post-edit") || strings.Contains(string(data), "cursor-pre-prompt") || strings.Contains(string(data), "cursor-session-start") {
		t.Errorf("hooks left after uninstall: %s", data)
	}
}

func TestInstallOpenCodePluginSyncsAfterEdit(t *testing.T) {
	dir := t.TempDir()
	if installed, err := InstallOpenCode(dir, "proj", 5); err != nil || !installed {
		t.Fatalf("InstallOpenCode = %v, %v", installed, err)
	}
	data, _ := os.ReadFile(OpenCodeDefaultPluginPath(dir))
	for _, want := range []string{`"tool.execute.after"`, `"chat.message"`, "--type opencode-pre-prompt", "--type opencode-post-edit", `const project = "proj"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plugin missing %q", want)
		}
	}
	if installed, _ := InstallOpenCode(dir, "proj", 5); installed {
		t.Error("second install should be a no-op")
	}
}
