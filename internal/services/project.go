package services

import (
	"fmt"
	"os"
	"path/filepath"

	"nav/config"
)

// ResolveProject determines the project name and repository path for a
// command.
//
// Both are optional. The project name comes from the first positional
// argument when one is given; otherwise it defaults to the basename of the
// current working directory. The repository path is resolved in priority
// order:
//
//  1. the --path flag (pathFlag) when non-empty,
//  2. the path registered for the project in ~/.nav-cli/projects.yaml,
//  3. the current working directory.
//
// The returned path is always absolute. The resolved (name, path) pair is
// persisted to projects.yaml so subsequent invocations can refer to the
// project by name alone.
func ResolveProject(args []string, pathFlag string) (name, path string, err error) {
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

// ResolveProjectByPath finds the project registered for repoPath — an exact
// match against ~/.nav-cli/projects.yaml — for entry points that only have a
// repository path and no project name to go on, chiefly nav's git hooks:
// git invokes them with no way to pass a project flag, so they used to sync
// every repo into a single shared "default" project/collection, mixing
// unrelated repos' embeddings together and never actually updating the
// collection a repo's own `nav index`/assistant hooks search. Falls back to
// the basename of repoPath (and registers it, exactly like ResolveProject
// does for an explicit invocation) when no registered project matches, so a
// repo not yet indexed by name still gets a stable, repo-specific project
// instead of the shared bucket.
func ResolveProjectByPath(repoPath string) string {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		abs = repoPath
	}

	if proj, ok := config.FindProjectByPath(abs); ok {
		return proj.Name
	}

	name := filepath.Base(abs)
	if err := config.AddProject(name, abs); err != nil {
		fmt.Fprintf(os.Stderr, "nav: warn: registering project %q: %v\n", name, err)
	}
	return name
}
