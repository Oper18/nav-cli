package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"nav/internal/db"
	"nav/internal/services"
)

var (
	graphSummaryPath string
	graphCallersPath string
	graphDepsPath    string
	graphSymbolPath  string

	graphCallersDepth int
	graphDepsDepth    int
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Query the per-project knowledge graph nav sync builds (packages, files, symbols, calls/imports)",
}

var graphSummaryCmd = &cobra.Command{
	Use:   "summary [project]",
	Short: "Print a ~1000-token digest: packages, entry points, top-called symbols",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runGraphSummary,
}

var graphCallersCmd = &cobra.Command{
	Use:   "callers <symbol> [project]",
	Short: "List callers of a symbol, walking the call graph backward",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runGraphCallers,
}

var graphDepsCmd = &cobra.Command{
	Use:   "deps <package|file> [project]",
	Short: "List a package or file's dependencies, walking imports forward",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runGraphDeps,
}

var graphSymbolCmd = &cobra.Command{
	Use:   "symbol <name> [project]",
	Short: "Show a symbol's definition location and direct edges",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runGraphSymbol,
}

func init() {
	graphSummaryCmd.Flags().StringVar(&graphSummaryPath, "path", "", "Path to the repository root (default: project path or current directory)")
	graphCallersCmd.Flags().StringVar(&graphCallersPath, "path", "", "Path to the repository root (default: project path or current directory)")
	graphDepsCmd.Flags().StringVar(&graphDepsPath, "path", "", "Path to the repository root (default: project path or current directory)")
	graphSymbolCmd.Flags().StringVar(&graphSymbolPath, "path", "", "Path to the repository root (default: project path or current directory)")

	graphCallersCmd.Flags().IntVar(&graphCallersDepth, "depth", 1, "How many call hops to walk backward")
	graphDepsCmd.Flags().IntVar(&graphDepsDepth, "depth", 3, "How many import hops to walk forward")

	graphCmd.AddCommand(graphSummaryCmd, graphCallersCmd, graphDepsCmd, graphSymbolCmd)
}

func runGraphSummary(cmd *cobra.Command, args []string) error {
	project, path, err := services.ResolveProject(args, graphSummaryPath)
	if err != nil {
		return err
	}
	digest, err := services.GraphSummaryDigest(project, path)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), digest)
	return nil
}

func runGraphCallers(cmd *cobra.Command, args []string) error {
	symbolName := args[0]
	project, path, err := services.ResolveProject(args[1:], graphCallersPath)
	if err != nil {
		return err
	}

	roots, results, err := services.GraphCallers(project, path, symbolName, graphCallersDepth)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if len(roots) == 0 {
		fmt.Fprintf(w, "no symbol named %q found in the graph\n", symbolName)
		return nil
	}

	fmt.Fprintf(w, "Callers of %s (depth %d):\n", symbolName, graphCallersDepth)
	if len(results) == 0 {
		fmt.Fprintln(w, "(none found)")
		return nil
	}
	for _, r := range results {
		fmt.Fprintf(w, "- [depth %d] %s:%d %s (%s)\n", r.Depth, r.File, r.Line, r.Name, r.Kind)
	}
	return nil
}

func runGraphDeps(cmd *cobra.Command, args []string) error {
	target := args[0]
	project, path, err := services.ResolveProject(args[1:], graphDepsPath)
	if err != nil {
		return err
	}

	rootID, node, results, err := services.GraphDeps(project, path, target, graphDepsDepth)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if rootID == "" {
		fmt.Fprintf(w, "no package or file named %q found in the graph\n", target)
		return nil
	}

	label := node.Name
	if node.Kind == db.KindFile {
		label = node.File
	}
	fmt.Fprintf(w, "Dependencies of %s (depth %d):\n", label, graphDepsDepth)
	if len(results) == 0 {
		fmt.Fprintln(w, "(none found)")
		return nil
	}
	for _, r := range results {
		l := r.Name
		if r.Kind == db.KindFile {
			l = r.File
		}
		fmt.Fprintf(w, "- [depth %d] %s (%s)\n", r.Depth, l, r.Kind)
	}
	return nil
}

func runGraphSymbol(cmd *cobra.Command, args []string) error {
	name := args[0]
	project, path, err := services.ResolveProject(args[1:], graphSymbolPath)
	if err != nil {
		return err
	}

	infos, err := services.GraphSymbol(project, path, name)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if len(infos) == 0 {
		fmt.Fprintf(w, "no symbol named %q found in the graph\n", name)
		return nil
	}

	for _, info := range infos {
		n := info.Node
		fmt.Fprintf(w, "%s (%s)\n  defined at %s:%d\n", n.Name, n.Kind, n.File, n.Line)
		if n.Summary != "" {
			fmt.Fprintf(w, "  summary: %s\n", n.Summary)
		}
		if len(info.Out) > 0 {
			fmt.Fprintln(w, "  outgoing:")
			for _, e := range info.Out {
				fmt.Fprintf(w, "    -%s-> %s\n", e.Kind, e.Dst)
			}
		}
		if len(info.In) > 0 {
			fmt.Fprintln(w, "  incoming:")
			for _, e := range info.In {
				fmt.Fprintf(w, "    %s -%s->\n", e.Src, e.Kind)
			}
		}
		fmt.Fprintln(w)
	}
	return nil
}
