package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "nav",
	Short: "Code navigation tool — index, search, and hook your codebase into AI context",
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(hookCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(repoCmd)

	hookCmd.AddCommand(hookInstallCmd)
	hookCmd.AddCommand(hookUninstallCmd)
	hookCmd.AddCommand(hookRunCmd)

	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configSetKeyCmd)

	repoCmd.AddCommand(repoFetchCmd)
	repoCmd.AddCommand(repoCleanBranchesCmd)
}
