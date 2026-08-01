package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nav/config"
)

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Hooks.GitSkipEnv = "NAV_SKIP"
	return cfg
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0755); err != nil {
		t.Fatalf("creating .git/hooks: %v", err)
	}
	return dir
}

func TestInstallWritesAllGitHooks(t *testing.T) {
	repo := initRepo(t)

	installed, err := Install(repo, testConfig())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !installed {
		t.Error("expected installed = true on a fresh repo")
	}

	for _, name := range []string{"pre-commit", "post-merge", "post-rewrite", "reference-transaction"} {
		path := filepath.Join(repo, ".git", "hooks", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("%s hook file is empty", name)
		}
	}

	// nav must not install a pre-push hook: indexing only ever runs on
	// commit and on pull, never on push (a push doesn't change what's on
	// disk, so there's nothing to (re-)index).
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-push")); !os.IsNotExist(err) {
		t.Errorf("expected no pre-push hook to be installed, stat err = %v", err)
	}

	// The post-rewrite hook must gate on $1 = "rebase" so it doesn't fire on
	// every `git commit --amend`, and must trigger the same sync path
	// post-merge uses ("git-post-merge" run type).
	rewrite, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", "post-rewrite"))
	if err != nil {
		t.Fatalf("reading post-rewrite: %v", err)
	}
	content := string(rewrite)
	if !strings.Contains(content, `"$1" = "rebase"`) {
		t.Errorf("post-rewrite hook missing rebase guard, got:\n%s", content)
	}
	if !strings.Contains(content, "--type git-post-merge") {
		t.Errorf("post-rewrite hook should trigger the git-post-merge run type, got:\n%s", content)
	}

	// The reference-transaction hook is what actually catches a fast-forward
	// `git pull` (post-merge/post-rewrite don't fire for it, since git skips
	// their machinery entirely on a pure fast-forward). It must gate on
	// $1 = "committed" and filter to the checked-out branch's own ref (or
	// HEAD), so a plain `git fetch` — which only moves remote-tracking
	// refs — doesn't trigger it.
	refTx, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", "reference-transaction"))
	if err != nil {
		t.Fatalf("reading reference-transaction: %v", err)
	}
	refContent := string(refTx)
	if !strings.Contains(refContent, `"$1" = "committed"`) {
		t.Errorf("reference-transaction hook missing committed guard, got:\n%s", refContent)
	}
	if !strings.Contains(refContent, "--type git-post-merge") {
		t.Errorf("reference-transaction hook should trigger the git-post-merge run type, got:\n%s", refContent)
	}
}

// TestReferenceTransactionHookFiresOnlyForCheckedOutBranch runs the
// installed reference-transaction script for real (it shells out to `git
// rev-parse`, so it needs a real git repo) against a fake `nav` on PATH
// that just records whether it was invoked, to verify the guard logic: only
// a "committed" transaction touching the checked-out branch's own ref (or
// HEAD) — never a "prepared" transaction, and never an unrelated
// remote-tracking ref update like a plain `git fetch` produces — reaches
// the `nav hook run` call.
func TestReferenceTransactionHookFiresOnlyForCheckedOutBranch(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	// Commit first so HEAD resolves to a real branch — an unborn branch's
	// --abbrev-ref behavior is inconsistent across git versions, and this
	// test only cares about the hook's ref-matching logic, not that edge
	// case.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	branch := strings.TrimSpace(git("symbolic-ref", "--short", "HEAD"))

	if _, err := Install(repo, testConfig()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	hookPath := filepath.Join(repo, ".git", "hooks", "reference-transaction")

	// A fake `nav` on PATH that just leaves a marker file behind when
	// invoked, so we can tell whether the hook script reached the
	// `nav hook run` line without needing a real nav binary or Qdrant/LLM
	// credentials.
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "called")
	fakeNav := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "nav"), []byte(fakeNav), 0755); err != nil {
		t.Fatalf("writing fake nav: %v", err)
	}

	run := func(t *testing.T, txState, stdin string) bool {
		t.Helper()
		os.Remove(marker)
		cmd := exec.Command(hookPath, txState)
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("running reference-transaction hook: %v\n%s", err, out)
		}
		_, err := os.Stat(marker)
		return err == nil
	}

	ownRef := "refs/heads/" + branch

	if called := run(t, "prepared", "old new "+ownRef+"\n"); called {
		t.Error("expected no nav call on a non-committed transaction")
	}
	if called := run(t, "committed", "old new refs/remotes/origin/"+branch+"\n"); called {
		t.Error("expected no nav call for an unrelated remote-tracking ref update")
	}
	if called := run(t, "committed", "old new "+ownRef+"\nold2 new2 refs/heads/other\n"); !called {
		t.Error("expected a nav call when the checked-out branch's own ref is updated")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	repo := initRepo(t)

	for i := 0; i < 3; i++ {
		installed, err := Install(repo, testConfig())
		if err != nil {
			t.Fatalf("Install run %d: %v", i, err)
		}
		wantInstalled := i == 0
		if installed != wantInstalled {
			t.Errorf("Install run %d: installed = %v, want %v", i, installed, wantInstalled)
		}
	}
}

func TestUninstallRemovesAllGitHooks(t *testing.T) {
	repo := initRepo(t)

	if _, err := Install(repo, testConfig()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall(repo); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	for _, name := range []string{"pre-commit", "post-merge", "post-rewrite", "reference-transaction"} {
		path := filepath.Join(repo, ".git", "hooks", name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err = %v", name, err)
		}
	}
}

// TestUninstallRemovesLegacyPrePushHook guards backward compatibility: an
// older nav version installed a pre-push hook that synced on push; current
// nav no longer installs one, but `nav hook uninstall` must still clean up
// one left behind by that older version.
func TestUninstallRemovesLegacyPrePushHook(t *testing.T) {
	repo := initRepo(t)
	path := filepath.Join(repo, ".git", "hooks", "pre-push")
	legacy := "#!/usr/bin/env bash\n# nav-hook\n# nav pre-push hook\nnav hook run --type git-pre-push --path \"$(git rev-parse --show-toplevel)\"\nexit 0\n"
	if err := os.WriteFile(path, []byte(legacy), 0755); err != nil {
		t.Fatalf("seeding legacy pre-push hook: %v", err)
	}

	if err := Uninstall(repo); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected legacy pre-push hook to be removed, stat err = %v", err)
	}
}

func TestInstallAppendsToExistingPostRewriteHook(t *testing.T) {
	repo := initRepo(t)
	existing := "#!/bin/sh\necho custom hook\n"
	path := filepath.Join(repo, ".git", "hooks", "post-rewrite")
	if err := os.WriteFile(path, []byte(existing), 0755); err != nil {
		t.Fatalf("seeding existing hook: %v", err)
	}

	if _, err := Install(repo, testConfig()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading post-rewrite: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "echo custom hook") {
		t.Error("expected the pre-existing hook content to be preserved")
	}
	if !strings.Contains(content, "nav-hook-append") {
		t.Error("expected the nav append marker to be present")
	}

	if err := Uninstall(repo); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading post-rewrite after uninstall: %v", err)
	}
	if string(after) != existing {
		t.Errorf("expected uninstall to leave only the original content, got:\n%s", after)
	}
}
