package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"nav/internal/services"
)

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List every registered project and whether it's actually been indexed",
	Long: "List every project registered in ~/.nav-cli/projects.yaml, alongside whether\n" +
		"it's actually been indexed (has a Qdrant collection) and its approximate\n" +
		"point count.\n\n" +
		"Registration and indexing are different things: a repo is auto-registered\n" +
		"the moment nav first resolves it — a git hook run, `nav index --path`, `nav\n" +
		"delete` — before any content has necessarily been embedded, so a project\n" +
		"can show up here as \"not indexed\" if that's as far as it's gotten.",
	Args: cobra.NoArgs,
	RunE: runList,
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")
}

func runList(cmd *cobra.Command, args []string) error {
	statuses, err := services.ListProjects(cmd.Context())
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()

	if listJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	}

	if len(statuses) == 0 {
		fmt.Fprintln(w, "No projects registered.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATH\tINDEXED\tPOINTS")
	for _, s := range statuses {
		indexed := "no"
		points := "-"
		if s.Indexed {
			indexed = "yes"
			points = fmt.Sprintf("%d", s.Points)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Path, indexed, points)
	}
	return tw.Flush()
}
