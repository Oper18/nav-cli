package services

import (
	"fmt"

	"nav/config"
)

// PrepareInit bootstraps the ~/.nav-cli config directory (creating it and
// writing default config/projects files if absent) and returns the loaded
// config, so a caller can seed interactive prompts (e.g. the current Qdrant
// host) with real defaults.
func PrepareInit() (*config.Config, error) {
	if err := config.EnsureDir(); err != nil {
		return nil, fmt.Errorf("ensuring config directory: %w", err)
	}
	if err := config.WriteDefault(); err != nil {
		return nil, fmt.Errorf("writing default config: %w", err)
	}
	if err := config.WriteDefaultProjects(); err != nil {
		return nil, fmt.Errorf("writing default projects: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return cfg, nil
}

// InitOptions carries the override values collected during `nav init`.
// Empty fields leave the existing stored value untouched.
type InitOptions struct {
	QdrantHost       string
	OpenRouterAPIKey string
	QdrantAPIKey     string
}

// ApplyInit stores the given credential overrides and returns the config
// directory path. QdrantHost is accepted for symmetry with the interactive
// prompt but, matching existing behaviour, is not persisted — nav has no
// config.Save for the main config file today, only for credentials.
func ApplyInit(overrides InitOptions) (dir string, err error) {
	creds, err := config.LoadCredentials()
	if err != nil {
		return "", fmt.Errorf("loading credentials: %w", err)
	}
	if overrides.OpenRouterAPIKey != "" {
		creds.OpenRouterAPIKey = overrides.OpenRouterAPIKey
	}
	if overrides.QdrantAPIKey != "" {
		creds.QdrantAPIKey = overrides.QdrantAPIKey
	}

	if err := config.SaveCredentials(creds); err != nil {
		return "", fmt.Errorf("saving credentials: %w", err)
	}
	return config.Dir(), nil
}
