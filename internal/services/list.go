package services

import (
	"context"
	"fmt"
	"sort"

	"nav/config"
	"nav/internal/db"
)

// ProjectStatus pairs one registered project's projects.yaml entry with its
// actual indexing state in Qdrant.
type ProjectStatus struct {
	Name    string
	Path    string
	Indexed bool  // whether the project's Qdrant collection ("nav_<name>") exists
	Points  int64 // approximate point count; 0 when Indexed is false
}

// ListProjects returns every project registered in ~/.nav-cli/projects.yaml,
// sorted by name, each paired with whether it's actually been indexed and
// its approximate point count. A project can be registered without ever
// having been indexed — e.g. ResolveProjectByPath auto-registers a repo the
// first time a git hook touches it, before any `nav index` has run — so
// registration alone doesn't mean "indexed."
func ListProjects(ctx context.Context) ([]ProjectStatus, error) {
	registered, err := config.LoadProjects()
	if err != nil {
		return nil, fmt.Errorf("loading projects: %w", err)
	}
	if len(registered.Projects) == 0 {
		return nil, nil
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}
	if err := EnsureLocalQdrant(cfg); err != nil {
		return nil, fmt.Errorf("ensuring local qdrant: %w", err)
	}
	qdrantClient, err := db.NewClient(cfg.Qdrant.Host, cfg.Qdrant.Port, creds.QdrantAPIKey, cfg.Qdrant.UseTLS)
	if err != nil {
		return nil, fmt.Errorf("creating qdrant client: %w", err)
	}
	defer qdrantClient.Close()

	statuses := make([]ProjectStatus, 0, len(registered.Projects))
	for _, p := range registered.Projects {
		points, indexed, err := qdrantClient.CollectionPointsCount(ctx, "nav_"+p.Name)
		if err != nil {
			return nil, fmt.Errorf("checking collection for %q: %w", p.Name, err)
		}
		statuses = append(statuses, ProjectStatus{Name: p.Name, Path: p.Path, Indexed: indexed, Points: points})
	}

	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses, nil
}
