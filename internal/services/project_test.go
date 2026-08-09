package services

import (
	"os"
	"path/filepath"
	"testing"

	"nav/config"
)

// chdir switches the process's working directory to dir for the duration of
// the test and restores the original on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

func TestResolveProjectExplicitNameMismatchedFromPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoDir := t.TempDir()
	chdir(t, repoDir)

	// Index-style call: an explicit project name that shares no part of the
	// repository path (basename included).
	name, path, err := ResolveProject([]string{"myproj"}, repoDir)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if name != "myproj" {
		t.Errorf("name = %q, want %q", name, "myproj")
	}
	absRepoDir, _ := filepath.Abs(repoDir)
	if path != absRepoDir {
		t.Errorf("path = %q, want %q", path, absRepoDir)
	}

	proj, ok := config.FindProject("myproj")
	if !ok || proj.Path != absRepoDir {
		t.Fatalf("FindProject(myproj) = %+v, %v; want path %q, true", proj, ok, absRepoDir)
	}
}

// TestResolveProjectNoNameFindsMismatchedProjectByPath is the regression
// case: once a project has been registered under a name that doesn't match
// any part of its path (as above), a later command run from inside that
// repo with no project name argument — the common case for `nav search`,
// `nav sync`, `nav graph ...`, `nav hook run ...` — must still resolve to
// the already-registered project rather than falling back to the directory
// basename and silently registering an unrelated duplicate project.
func TestResolveProjectNoNameFindsMismatchedProjectByPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoDir := t.TempDir()
	absRepoDir, _ := filepath.Abs(repoDir)
	chdir(t, repoDir)

	if _, _, err := ResolveProject([]string{"myproj"}, repoDir); err != nil {
		t.Fatalf("ResolveProject (initial index): %v", err)
	}

	// No project name, no --path: mirrors `nav search "query"` run from
	// inside the repo.
	name, path, err := ResolveProject(nil, "")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if name != "myproj" {
		t.Errorf("name = %q, want %q (the registered project for this path)", name, "myproj")
	}
	if path != absRepoDir {
		t.Errorf("path = %q, want %q", path, absRepoDir)
	}

	// Must not have registered a spurious second project named after the
	// directory basename.
	if _, ok := config.FindProject(filepath.Base(absRepoDir)); ok {
		t.Errorf("expected no project registered under the directory basename %q", filepath.Base(absRepoDir))
	}

	projects, err := config.LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects.Projects) != 1 {
		t.Errorf("expected exactly one registered project, got %+v", projects.Projects)
	}
}

func TestResolveProjectNoNameNoRegisteredPathFallsBackToBasename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoDir := t.TempDir()
	absRepoDir, _ := filepath.Abs(repoDir)
	chdir(t, repoDir)

	name, path, err := ResolveProject(nil, "")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if name != filepath.Base(absRepoDir) {
		t.Errorf("name = %q, want basename %q", name, filepath.Base(absRepoDir))
	}
	if path != absRepoDir {
		t.Errorf("path = %q, want %q", path, absRepoDir)
	}
}
