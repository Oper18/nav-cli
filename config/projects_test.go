package config

import "testing"

func TestAddFindRemoveProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := AddProject("alpha", "/repos/alpha"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := AddProject("beta", "/repos/beta"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	if proj, ok := FindProject("alpha"); !ok || proj.Path != "/repos/alpha" {
		t.Fatalf("FindProject(alpha) = %+v, %v; want path /repos/alpha, true", proj, ok)
	}
	if _, ok := FindProject("missing"); ok {
		t.Error("FindProject(missing) = true, want false")
	}

	if err := RemoveProject("alpha"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if _, ok := FindProject("alpha"); ok {
		t.Error("expected alpha to be gone after RemoveProject")
	}
	if proj, ok := FindProject("beta"); !ok || proj.Path != "/repos/beta" {
		t.Errorf("expected beta to survive removing alpha, got %+v, %v", proj, ok)
	}

	// Removing an already-absent (or never-registered) project is a no-op,
	// not an error.
	if err := RemoveProject("alpha"); err != nil {
		t.Errorf("RemoveProject (already gone): %v", err)
	}
	if err := RemoveProject("never-registered"); err != nil {
		t.Errorf("RemoveProject (never registered): %v", err)
	}
}

func TestFindProjectByPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := AddProject("alpha", "/repos/alpha"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := AddProject("beta", "/repos/beta"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	proj, ok := FindProjectByPath("/repos/beta")
	if !ok || proj.Name != "beta" {
		t.Errorf("FindProjectByPath(/repos/beta) = %+v, %v; want name beta, true", proj, ok)
	}

	if _, ok := FindProjectByPath("/repos/does-not-exist"); ok {
		t.Error("FindProjectByPath should not match an unregistered path")
	}

	// A path that is merely a prefix/suffix of a registered one must not match.
	if _, ok := FindProjectByPath("/repos/alpha/nested"); ok {
		t.Error("FindProjectByPath must require an exact match, not a prefix")
	}
	if _, ok := FindProjectByPath("/repos/alph"); ok {
		t.Error("FindProjectByPath must require an exact match, not a partial name")
	}
}

func TestAddProjectUpdatesExistingEntryPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := AddProject("alpha", "/repos/alpha-old"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := AddProject("alpha", "/repos/alpha-new"); err != nil {
		t.Fatalf("AddProject (update): %v", err)
	}

	proj, ok := FindProject("alpha")
	if !ok || proj.Path != "/repos/alpha-new" {
		t.Errorf("FindProject(alpha) = %+v, %v; want updated path /repos/alpha-new, true", proj, ok)
	}

	projects, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects.Projects) != 1 {
		t.Errorf("expected re-adding the same name to update in place, not duplicate; got %+v", projects.Projects)
	}
}
