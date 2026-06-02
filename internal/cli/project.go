package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"nav/config"
)

// resolveProject determines the project name and repository path for a command.
//
// Both are optional. The project name comes from the first positional argument
// when one is given; otherwise it defaults to the basename of the current
// working directory. The repository path is resolved in priority order:
//
//  1. the --path flag (pathFlag) when non-empty,
//  2. the path registered for the project in ~/.nav-cli/projects.yaml,
//  3. the current working directory.
//
// The returned path is always absolute. The resolved (name, path) pair is
// persisted to projects.yaml so subsequent invocations can refer to the project
// by name alone.
func resolveProject(args []string, pathFlag string) (name, path string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("determining current directory: %w", err)
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return "", "", fmt.Errorf("resolving current directory: %w", err)
	}

	// Project name: positional argument, or the current directory's basename.
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	} else {
		name = filepath.Base(cwdAbs)
	}

	// Repository path: --path flag, then registered path, then current directory.
	switch {
	case pathFlag != "":
		abs, err := filepath.Abs(pathFlag)
		if err != nil {
			return "", "", fmt.Errorf("resolving --path %q: %w", pathFlag, err)
		}
		path = abs
	default:
		if proj, ok := config.FindProject(name); ok && proj.Path != "" {
			path = proj.Path
		} else {
			path = cwdAbs
		}
	}

	// Persist so the project can later be referenced by name alone.
	if err := config.AddProject(name, path); err != nil {
		return "", "", fmt.Errorf("registering project: %w", err)
	}
	return name, path, nil
}
