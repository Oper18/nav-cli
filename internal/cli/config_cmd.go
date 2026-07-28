package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"nav/internal/services"
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
	yaml, err := services.ShowConfigYAML()
	if err != nil {
		return err
	}
	fmt.Print(yaml)
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
	key, value := args[0], args[1]
	if err := services.SetConfigKey(key, value); err != nil {
		return err
	}
	fmt.Printf("Set %s = %s\n", key, value)
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
	provider, apiKey := strings.ToLower(args[0]), args[1]
	if err := services.SetCredentialKey(provider, apiKey); err != nil {
		return err
	}
	fmt.Printf("Stored %s API key in %s\n", provider, services.CredentialsPath())
	return nil
}
