package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstallOpenCode creates an OpenCode plugin that provides a nav_context tool.
func InstallOpenCode(dir, project string, topK int) error {
	opencodeDir := filepath.Join(dir, ".opencode")
	pluginDir := filepath.Join(opencodeDir, "plugins")

	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("creating plugin directory: %w", err)
	}

	// Create package.json if it doesn't exist to ensure @opencode-ai/plugin is available
	pkgPath := filepath.Join(opencodeDir, "package.json")
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		pkgContent := `{"dependencies": {"@opencode-ai/plugin": "latest"}}`
		if err := os.WriteFile(pkgPath, []byte(pkgContent), 0644); err != nil {
			return fmt.Errorf("creating package.json: %w", err)
		}
	}

	pluginPath := filepath.Join(pluginDir, "nav-hook.js")

	// Check if already installed by reading the file
	if data, err := os.ReadFile(pluginPath); err == nil {
		if strings.Contains(string(data), "nav hook run") && strings.Contains(string(data), "--type opencode") {
			return nil // already installed
		}
	}

	pluginContent := fmt.Sprintf(`import { tool } from "@opencode-ai/plugin";

export const NavHookPlugin = async ({ directory, $ }) => {
  return {
    tool: {
      nav_context: tool({
        description: "Search the codebase for relevant context using the nav CLI tool. Use this to understand the codebase before answering.",
        args: {
          query: tool.schema.string({ description: "The search query to find relevant code context" })
        },
        async execute(args, context) {
          const project = "%s";
          const topK = %d;
          try {
            const result = await $` + "`nav hook run ${project} --type opencode --top ${topK} --query ${args.query}`" + `;
            return result.text;
          } catch (error) {
            return "Error running nav hook: " + error.message;
          }
        }
      })
    }
  }
};
`, project, topK)

	return os.WriteFile(pluginPath, []byte(pluginContent), 0644)
}

// UninstallOpenCode removes the nav plugin from OpenCode.
func UninstallOpenCode(dir string) error {
	pluginPath := filepath.Join(dir, ".opencode", "plugins", "nav-hook.js")
	if err := os.Remove(pluginPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plugin: %w", err)
	}
	return nil
}

// OpenCodeDefaultPluginPath returns the path to the nav plugin in dir.
func OpenCodeDefaultPluginPath(dir string) string {
	return filepath.Join(dir, ".opencode", "plugins", "nav-hook.js")
}
