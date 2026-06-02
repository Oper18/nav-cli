package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"nav/config"
)

// ---------------------------------------------------------------------------
// Top-level config command
// ---------------------------------------------------------------------------

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and modify nav configuration",
}

// ---------------------------------------------------------------------------
// config show
// ---------------------------------------------------------------------------

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print current configuration",
	RunE:  runConfigShow,
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	fmt.Print(string(data))
	return nil
}

// ---------------------------------------------------------------------------
// config set <key> <value>
// ---------------------------------------------------------------------------

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key := args[0]
	value := args[1]

	cfgPath := filepath.Join(config.Dir(), "config.yaml")

	// Read the existing config file (or start from empty map if absent).
	var raw map[string]interface{}
	data, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing config file: %w", err)
		}
	}
	if raw == nil {
		raw = make(map[string]interface{})
	}

	// Set the key using dot-separated path.
	if err := setNestedKey(raw, key, value); err != nil {
		return fmt.Errorf("setting key %q: %w", key, err)
	}

	// Write back.
	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshalling updated config: %w", err)
	}
	if err := os.WriteFile(cfgPath, out, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return nil
}

// setNestedKey traverses m by the dot-separated path in key and sets the leaf
// to value, creating intermediate maps as needed.
func setNestedKey(m map[string]interface{}, key, value string) error {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 1 {
		m[key] = value
		return nil
	}
	head, tail := parts[0], parts[1]
	child, ok := m[head]
	if !ok || child == nil {
		child = make(map[string]interface{})
	}
	childMap, ok := child.(map[string]interface{})
	if !ok {
		// The existing value at this level is a scalar — replace with a map.
		childMap = make(map[string]interface{})
	}
	if err := setNestedKey(childMap, tail, value); err != nil {
		return err
	}
	m[head] = childMap
	return nil
}

// ---------------------------------------------------------------------------
// config set-key <provider> <api-key>
// ---------------------------------------------------------------------------

var configSetKeyCmd = &cobra.Command{
	Use:   "set-key <provider> <api-key>",
	Short: "Store an API key in ~/.nav-cli/credentials",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSetKey,
}

func runConfigSetKey(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(args[0])
	apiKey := args[1]

	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	switch provider {
	case "openrouter":
		creds.OpenRouterAPIKey = apiKey
	case "qdrant":
		creds.QdrantAPIKey = apiKey
	default:
		return fmt.Errorf("unknown provider %q; choose from: openrouter, qdrant", provider)
	}

	if err := config.SaveCredentials(creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Printf("Stored %s API key in %s\n", provider, filepath.Join(config.Dir(), "credentials"))
	return nil
}
