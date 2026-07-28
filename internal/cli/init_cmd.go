package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"nav/internal/services"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap ~/.nav-cli config directory",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	cfg, err := services.PrepareInit()
	if err != nil {
		return err
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

	qdrantHost, err := prompt(fmt.Sprintf("Qdrant host [%s]: ", cfg.Qdrant.Host))
	if err != nil {
		return fmt.Errorf("reading qdrant host: %w", err)
	}

	orKey, err := prompt("OpenRouter API key: ")
	if err != nil {
		return fmt.Errorf("reading OpenRouter API key: %w", err)
	}

	qdKey, err := prompt("Qdrant API key (leave empty to skip): ")
	if err != nil {
		return fmt.Errorf("reading Qdrant API key: %w", err)
	}

	dir, err := services.ApplyInit(services.InitOptions{
		QdrantHost:       qdrantHost,
		OpenRouterAPIKey: orKey,
		QdrantAPIKey:     qdKey,
	})
	if err != nil {
		return err
	}

	fmt.Printf("nav initialised at %s\n", dir)
	return nil
}
