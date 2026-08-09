package services

import (
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// initTestGitRepo creates a temp git repo with the given tracked files
// committed and the given untracked/ignored files left on disk, and returns
// its path. Skips the test when git isn't available.
func initTestGitRepo(t *testing.T, tracked map[string]string, untracked map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	for rel, content := range tracked {
		mustWriteFile(t, filepath.Join(repo, rel), content)
	}
	if len(tracked) > 0 {
		run("add", ".")
		run("commit", "-q", "-m", "init")
	}
	for rel, content := range untracked {
		mustWriteFile(t, filepath.Join(repo, rel), content)
	}

	return repo
}

func TestIsGitRepo(t *testing.T) {
	repo := initTestGitRepo(t, map[string]string{"main.go": "package main\n"}, nil)

	if !IsGitRepo(repo) {
		t.Error("IsGitRepo(repo) = false, want true")
	}

	notRepo := t.TempDir()
	if IsGitRepo(notRepo) {
		t.Error("IsGitRepo(notRepo) = true, want false")
	}
}

func TestGitTrackedFilesExcludesIgnoredAndUntracked(t *testing.T) {
	repo := initTestGitRepo(t,
		map[string]string{
			"main.go":             "package main\n",
			"internal/service.go": "package internal\n",
			".gitignore":          "node_modules/\n",
		},
		map[string]string{
			"node_modules/pkg/index.js": "module.exports = {}\n",
			"scratch.tmp":               "not added",
		},
	)

	got, err := GitTrackedFiles(repo)
	if err != nil {
		t.Fatalf("GitTrackedFiles: %v", err)
	}
	sort.Strings(got)

	want := []string{".gitignore", "internal/service.go", "main.go"}
	if len(got) != len(want) {
		t.Fatalf("GitTrackedFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GitTrackedFiles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestIndexSpecificFilesDiscoveryPrefersGitTrackedFiles is a narrow,
// dependency-free check on the file-discovery step itself (not the full
// IndexSpecificFiles pipeline, which needs Qdrant/an LLM key): a git repo's
// gitignored vendor tree must never reach the candidate file list, exactly
// the class of bug ShouldSkip's nested-pattern fix and this git-first
// discovery were both introduced to close.
func TestIndexSpecificFilesDiscoveryPrefersGitTrackedFiles(t *testing.T) {
	repo := initTestGitRepo(t,
		map[string]string{
			"main.go":    "package main\n",
			".gitignore": "node_modules/\n",
		},
		map[string]string{
			"node_modules/pkg/index.js": "module.exports = {}\n",
		},
	)

	if !IsGitRepo(repo) {
		t.Fatal("expected repo to be detected as a git repo")
	}
	tracked, err := GitTrackedFiles(repo)
	if err != nil {
		t.Fatalf("GitTrackedFiles: %v", err)
	}
	for _, rel := range tracked {
		if rel == "node_modules/pkg/index.js" {
			t.Fatalf("GitTrackedFiles leaked a gitignored vendor file: %v", tracked)
		}
	}

	// A directory with no .git at all falls back to the raw filesystem walk,
	// which has no gitignore to lean on — only cfg.Indexing.SkipPatterns
	// (applied downstream via parser.ShouldSkip) keeps vendor dirs out there.
	plainDir := t.TempDir()
	if IsGitRepo(plainDir) {
		t.Fatal("expected a plain temp dir not to be detected as a git repo")
	}
	mustWriteFile(t, filepath.Join(plainDir, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(plainDir, "node_modules/pkg/index.js"), "module.exports = {}\n")
	walked, err := walkRepoFiles(plainDir, nil)
	if err != nil {
		t.Fatalf("walkRepoFiles: %v", err)
	}
	foundVendor := false
	for _, rel := range walked {
		if filepath.ToSlash(rel) == "node_modules/pkg/index.js" {
			foundVendor = true
		}
	}
	if !foundVendor {
		t.Fatal("walkRepoFiles should still surface vendor files (patterns filter them downstream, not the walk)")
	}
}
