package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"nav/internal/db"
)

// migratedProjects tracks which projects this process has already attempted
// to migrate, so repeated calls (many hooks/searches per process, or many
// projects sharing this process for a long-running command) don't re-stat
// the legacy location on every db touch.
var migratedProjects sync.Map

// ensureMigratedFromRepo moves a project's legacy <repoPath>/.nav directory
// (nav's old, pre-1.x storage location, keyed by repo path instead of
// project name) into its new home under ~/.nav/projects/<project>/, the
// first time this process touches that project's database. It's a one-time,
// best-effort move — a project with nothing at the legacy location, or whose
// new-location directory already has its own state, is left untouched.
//
// This exists purely to avoid a wholesale re-summarise/re-embed of every
// already-indexed project the first time this version of nav runs against
// it: without carrying the manifest over, the fresh (empty) database at the
// new location would make every previously-synced symbol look dirty again,
// even though Qdrant already has it — exactly the wasted-token problem
// that's motivated most of nav's recent changes.
func ensureMigratedFromRepo(project, repoPath string) {
	if _, done := migratedProjects.LoadOrStore(project, true); done {
		return
	}

	legacyDir := filepath.Join(repoPath, ".nav")
	info, err := os.Stat(legacyDir)
	if err != nil || !info.IsDir() {
		return // nothing to migrate
	}

	newDir, err := db.Dir(project)
	if err != nil {
		return
	}

	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return
	}
	moved := 0
	for _, e := range entries {
		name := e.Name()
		if name == ".gitignore" {
			continue // legacy artefact of the old in-repo location; not needed here
		}
		oldPath := filepath.Join(legacyDir, name)
		newPath := filepath.Join(newDir, name)
		if _, err := os.Stat(newPath); err == nil {
			continue // new location already has this file — never clobber it
		}
		if err := moveFile(oldPath, newPath); err != nil {
			fmt.Fprintf(os.Stderr, "nav: warn: migrating %s to %s: %v\n", oldPath, newPath, err)
			continue
		}
		moved++
	}
	if moved > 0 {
		fmt.Fprintf(os.Stderr, "nav: migrated local index state for %q from %s to %s\n", project, legacyDir, newDir)
	}
}

// moveFile moves oldPath to newPath, preferring a plain rename but falling
// back to copy-then-remove when the two paths are on different filesystems —
// os.Rename refuses to cross a device boundary (EXDEV, surfaced on Linux as
// "invalid cross-device link"), which is the common case here: repos often
// live under /tmp or a separate mount, while ~/.nav/projects lives on the
// home filesystem.
func moveFile(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}

	src, err := os.Open(oldPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", oldPath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("creating %s: %w", newPath, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(newPath) // don't leave a truncated partial copy behind
		return fmt.Errorf("copying %s to %s: %w", oldPath, newPath, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", newPath, err)
	}

	if err := os.Remove(oldPath); err != nil {
		return fmt.Errorf("removing %s after copy: %w", oldPath, err)
	}
	return nil
}

// openProjectDB migrates project's legacy in-repo state if present, then
// opens branch's database at its new home. Every internal service that used
// to call db.Open(repoPath, branch) directly should call this instead, so
// the migration always runs before the database it would have carried state
// into is touched.
func openProjectDB(project, repoPath, branch string) (*db.DB, error) {
	ensureMigratedFromRepo(project, repoPath)
	return db.Open(project, branch)
}
