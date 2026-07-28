package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"nav/config"
)

// ShowConfigYAML loads the current configuration and renders it as YAML.
func ShowConfigYAML() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshalling config: %w", err)
	}
	return string(data), nil
}

// SetConfigKey sets a dot-separated key path in ~/.nav-cli/config.yaml to
// value, creating intermediate maps as needed, and writes the file back.
func SetConfigKey(key, value string) error {
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

	if err := setNestedKey(raw, key, value); err != nil {
		return fmt.Errorf("setting key %q: %w", key, err)
	}

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshalling updated config: %w", err)
	}
	if err := os.WriteFile(cfgPath, out, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
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

// CredentialsPath returns the path credentials are stored at, for display
// purposes.
func CredentialsPath() string {
	return filepath.Join(config.Dir(), "credentials")
}

// SetCredentialKey stores apiKey for provider ("openrouter" or "qdrant") in
// ~/.nav-cli/credentials.
func SetCredentialKey(provider, apiKey string) error {
	provider = strings.ToLower(provider)

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
	return nil
}
