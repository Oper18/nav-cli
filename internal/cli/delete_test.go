package cli

import (
	"testing"

	"nav/config"
)

// resetDeleteFlags restores the delete command's package-level flag
// variables to their zero values, since cobra flags are shared mutable
// package state and tests run in the same process.
func resetDeleteFlags(t *testing.T) {
	t.Helper()
	deletePath = ""
	t.Cleanup(func() { deletePath = "" })
}

func TestResolveDeleteTargetExplicitName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetDeleteFlags(t)

	if err := config.AddProject("alpha", "/repos/alpha"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	project, repoPath, err := resolveDeleteTarget([]string{"alpha"})
	if err != nil {
		t.Fatalf("resolveDeleteTarget: %v", err)
	}
	if project != "alpha" || repoPath != "/repos/alpha" {
		t.Errorf("resolveDeleteTarget = %q, %q; want alpha, /repos/alpha", project, repoPath)
	}
}

func TestResolveDeleteTargetExplicitNameNotRegistered(t *testing.T) {
	// An explicit name that isn't registered is accepted as-is (no
	// registered path to also clean up, but the name itself is trusted) —
	// DeleteProject's individual steps are each safe no-ops against
	// nonexistent state.
	t.Setenv("HOME", t.TempDir())
	resetDeleteFlags(t)

	project, repoPath, err := resolveDeleteTarget([]string{"never-registered"})
	if err != nil {
		t.Fatalf("resolveDeleteTarget: %v", err)
	}
	if project != "never-registered" || repoPath != "" {
		t.Errorf("resolveDeleteTarget = %q, %q; want never-registered, \"\"", project, repoPath)
	}
}

func TestResolveDeleteTargetByPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetDeleteFlags(t)

	if err := config.AddProject("backend", "/repos/backend"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	deletePath = "/repos/backend"

	project, repoPath, err := resolveDeleteTarget(nil)
	if err != nil {
		t.Fatalf("resolveDeleteTarget: %v", err)
	}
	if project != "backend" || repoPath != "/repos/backend" {
		t.Errorf("resolveDeleteTarget = %q, %q; want backend, /repos/backend", project, repoPath)
	}
}

func TestResolveDeleteTargetByPathUnregisteredIsAnError(t *testing.T) {
	// Critical safety property: an unmatched path must error, never silently
	// fall back to "delete whatever this directory's basename would be" —
	// that could target a completely unrelated project that happens to
	// share a name, and must never register the very entry it's about to
	// remove as a side effect of merely resolving the target.
	t.Setenv("HOME", t.TempDir())
	resetDeleteFlags(t)
	deletePath = "/repos/never-indexed"

	_, _, err := resolveDeleteTarget(nil)
	if err == nil {
		t.Fatal("expected an error for an unregistered path, got nil")
	}

	if _, ok := config.FindProject("never-indexed"); ok {
		t.Error("resolveDeleteTarget must not register a project as a side effect")
	}
}
