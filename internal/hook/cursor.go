package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const cursorHookEvent = "beforeSubmitPrompt"

// CursorHooksFile is the top-level structure of .cursor/hooks.json (schema version 1).
type CursorHooksFile struct {
	Version int                        `json:"version"`
	Hooks   map[string][]CursorHookDef `json:"hooks"`
}

// CursorHookDef is one hook registration for a Cursor agent event.
type CursorHookDef struct {
	Command    string `json:"command"`
	Timeout    int    `json:"timeout,omitempty"`
	FailClosed bool   `json:"failClosed,omitempty"`
}

// CursorPromptInput is the JSON Cursor sends on stdin for beforeSubmitPrompt.
type CursorPromptInput struct {
	Prompt        string   `json:"prompt"`
	HookEventName string   `json:"hook_event_name"`
	Workspace     []string `json:"workspace_roots"`
}

// CursorSubmitResponse is the JSON nav writes to stdout for beforeSubmitPrompt.
type CursorSubmitResponse struct {
	Continue          bool   `json:"continue"`
	UserMessage       string `json:"user_message,omitempty"`
	AdditionalContext string `json:"additional_context,omitempty"`
}

// InstallCursor registers a beforeSubmitPrompt hook in .cursor/hooks.json that invokes
// nav hook run <project> --type cursor. hooksPath is the full path to hooks.json.
func InstallCursor(hooksPath, project string, topK int) error {
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		return fmt.Errorf("creating .cursor directory: %w", err)
	}

	file, err := readCursorHooksJSON(hooksPath)
	if err != nil {
		return err
	}

	if file.Hooks == nil {
		file.Hooks = make(map[string][]CursorHookDef)
	}
	if file.Version == 0 {
		file.Version = 1
	}

	navCommand := fmt.Sprintf("nav hook run %s --type cursor --top %d", project, topK)

	for _, def := range file.Hooks[cursorHookEvent] {
		if entryContainsNavCursor(def) {
			return nil // already installed
		}
	}

	file.Hooks[cursorHookEvent] = append(file.Hooks[cursorHookEvent], CursorHookDef{
		Command:    navCommand,
		Timeout:    15,
		FailClosed: false,
	})

	return writeCursorHooksJSON(hooksPath, file)
}

// UninstallCursor removes nav cursor hook entries from .cursor/hooks.json.
func UninstallCursor(hooksPath string) error {
	file, err := readCursorHooksJSON(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	existing := file.Hooks[cursorHookEvent]
	if len(existing) == 0 {
		return nil
	}

	filtered := make([]CursorHookDef, 0, len(existing))
	for _, def := range existing {
		if !entryContainsNavCursor(def) {
			filtered = append(filtered, def)
		}
	}
	file.Hooks[cursorHookEvent] = filtered

	return writeCursorHooksJSON(hooksPath, file)
}

// DefaultCursorHooksPath returns <dir>/.cursor/hooks.json.
func DefaultCursorHooksPath(dir string) string {
	return filepath.Join(dir, ".cursor", "hooks.json")
}

// GlobalCursorHooksPath returns ~/.cursor/hooks.json.
func GlobalCursorHooksPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cursor", "hooks.json")
	}
	return filepath.Join(home, ".cursor", "hooks.json")
}

// ReadCursorPromptFromStdin parses the beforeSubmitPrompt payload Cursor passes on stdin.
func ReadCursorPromptFromStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	if len(data) == 0 {
		return "", nil
	}
	var input CursorPromptInput
	if err := json.Unmarshal(data, &input); err != nil {
		return "", fmt.Errorf("parsing cursor hook input: %w", err)
	}
	return strings.TrimSpace(input.Prompt), nil
}

// FormatCursorSubmitResponse serialises the hook response Cursor expects on stdout.
func FormatCursorSubmitResponse(continuePrompt bool, additionalContext string) (string, error) {
	resp := CursorSubmitResponse{
		Continue:          continuePrompt,
		AdditionalContext: additionalContext,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("marshalling cursor hook response: %w", err)
	}
	return string(data), nil
}

func readCursorHooksJSON(path string) (*CursorHooksFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &CursorHooksFile{Version: 1, Hooks: make(map[string][]CursorHookDef)}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var file CursorHooksFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if file.Hooks == nil {
		file.Hooks = make(map[string][]CursorHookDef)
	}
	return &file, nil
}

func writeCursorHooksJSON(path string, file *CursorHooksFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling hooks: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func entryContainsNavCursor(def CursorHookDef) bool {
	return strings.Contains(def.Command, "nav hook run") && strings.Contains(def.Command, "--type cursor")
}
