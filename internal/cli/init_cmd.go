package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"nav/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap ~/.nav-cli config directory",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	// 1. Create directory structure.
	if err := config.EnsureDir(); err != nil {
		return fmt.Errorf("ensuring config directory: %w", err)
	}

	// 2. Write default config and projects file if absent.
	if err := config.WriteDefault(); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}
	if err := config.WriteDefaultProjects(); err != nil {
		return fmt.Errorf("writing default projects: %w", err)
	}

	// 3. Load existing config and credentials for interactive prompts.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	reader := bufio.NewReader(os.Stdin)

	// Helper: print a prompt, read a line, and return it trimmed.
	prompt := func(label string) (string, error) {
		fmt.Print(label)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	// Qdrant host
	hostPrompt := fmt.Sprintf("Qdrant host [%s]: ", cfg.Qdrant.Host)
	qdrantHost, err := prompt(hostPrompt)
	if err != nil {
		return fmt.Errorf("reading qdrant host: %w", err)
	}
	if qdrantHost != "" {
		cfg.Qdrant.Host = qdrantHost
	}

	// OpenRouter API key
	orKey, err := prompt("OpenRouter API key: ")
	if err != nil {
		return fmt.Errorf("reading OpenRouter API key: %w", err)
	}
	if orKey != "" {
		creds.OpenRouterAPIKey = orKey
	}

	// Qdrant API key (optional)
	qdKey, err := prompt("Qdrant API key (leave empty to skip): ")
	if err != nil {
		return fmt.Errorf("reading Qdrant API key: %w", err)
	}
	if qdKey != "" {
		creds.QdrantAPIKey = qdKey
	}

	// 4. Save credentials.
	if err := config.SaveCredentials(creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Printf("nav initialised at %s\n", config.Dir())
	return nil
}
