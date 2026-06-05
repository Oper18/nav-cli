package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Project is a single entry in projects.yaml mapping a project name to the
// absolute path of its repository root.
type Project struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// Projects is the root structure of ~/.nav-cli/projects.yaml.
type Projects struct {
	Projects []Project `yaml:"projects"`
}

// ProjectsPath returns the path to ~/.nav-cli/projects.yaml.
func ProjectsPath() string {
	return filepath.Join(Dir(), "projects.yaml")
}

// ProjectDir returns the per-project directory ~/.nav-cli/projects/<name>, which
// holds project-scoped artefacts such as the generated readme.md.
func ProjectDir(name string) string {
	return filepath.Join(Dir(), "projects", name)
}

// ProjectReadmePath returns the path to ~/.nav-cli/projects/<name>/readme.md.
func ProjectReadmePath(name string) string {
	return filepath.Join(ProjectDir(name), "readme.md")
}

// ReadProjectReadme returns the contents of the project's generated README, or
// an empty string when none has been written yet. A missing file is not an
// error: callers treat the absence as "no project context available".
func ReadProjectReadme(name string) (string, error) {
	data, err := os.ReadFile(ProjectReadmePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading project readme: %w", err)
	}
	return string(data), nil
}

// WriteProjectReadme writes the generated README markdown to
// ~/.nav-cli/projects/<name>/readme.md, creating the project directory if needed.
func WriteProjectReadme(name, content string) error {
	dir := ProjectDir(name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating project directory %s: %w", dir, err)
	}
	if err := os.WriteFile(ProjectReadmePath(name), []byte(content), 0644); err != nil {
		return fmt.Errorf("writing project readme: %w", err)
	}
	return nil
}

// LoadProjects reads ~/.nav-cli/projects.yaml. A missing file yields an empty
// list rather than an error.
func LoadProjects() (*Projects, error) {
	data, err := os.ReadFile(ProjectsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Projects{}, nil
		}
		return nil, fmt.Errorf("reading projects: %w", err)
	}
	var p Projects
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshalling projects: %w", err)
	}
	return &p, nil
}

// SaveProjects writes the projects list to ~/.nav-cli/projects.yaml.
func SaveProjects(p *Projects) error {
	if err := EnsureDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshalling projects: %w", err)
	}
	if err := os.WriteFile(ProjectsPath(), data, 0644); err != nil {
		return fmt.Errorf("writing projects: %w", err)
	}
	return nil
}

// WriteDefaultProjects writes an empty projects.yaml scaffold to ~/.nav-cli/
// only when the file does not already exist.
func WriteDefaultProjects() error {
	if err := EnsureDir(); err != nil {
		return err
	}
	path := ProjectsPath()
	if _, err := os.Stat(path); err == nil {
		// File already exists — do not overwrite.
		return nil
	}
	return SaveProjects(&Projects{Projects: []Project{}})
}

// FindProject returns the project with the given name, if it is registered.
func FindProject(name string) (Project, bool) {
	projects, err := LoadProjects()
	if err != nil {
		return Project{}, false
	}
	for _, proj := range projects.Projects {
		if proj.Name == name {
			return proj, true
		}
	}
	return Project{}, false
}

// AddProject registers (or updates) a project entry in projects.yaml. When an
// entry with the same name already exists its path is refreshed only when the
// supplied path is non-empty.
func AddProject(name, path string) error {
	projects, err := LoadProjects()
	if err != nil {
		return err
	}
	for i := range projects.Projects {
		if projects.Projects[i].Name == name {
			if path != "" {
				projects.Projects[i].Path = path
			}
			return SaveProjects(projects)
		}
	}
	projects.Projects = append(projects.Projects, Project{Name: name, Path: path})
	return SaveProjects(projects)
}
