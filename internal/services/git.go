package services

import (
	"os/exec"
	"strings"
)

// RunGitCmd executes a git command inside repoPath and returns stdout.
func RunGitCmd(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SplitLines splits newline-delimited output and drops empty lines.
func SplitLines(raw string) []string {
	parts := strings.Split(raw, "\n")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// CurrentBranch returns the current git branch in repoPath, or "" if it
// cannot be determined (detached HEAD or non-git directory).
func CurrentBranch(repoPath string) string {
	out, err := RunGitCmd(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HeadCommit returns the current HEAD commit hash, or "" if it cannot be
// determined (detached/empty repo).
func HeadCommit(repoPath string) string {
	out, err := RunGitCmd(repoPath, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// GitStatusFiles runs `git status --porcelain -uall` and splits the result
// into changed (added/modified/untracked/renamed-to) and deleted
// (removed/renamed-from) paths, relative to repoPath.
func GitStatusFiles(repoPath string) (changed, deleted []string, err error) {
	out, err := RunGitCmd(repoPath, "status", "--porcelain", "-uall")
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		status := line[:2]
		rest := strings.TrimSpace(line[3:])
		path := rest
		if idx := strings.Index(rest, " -> "); idx >= 0 {
			oldPath := unquoteGitPath(rest[:idx])
			path = rest[idx+len(" -> "):]
			deleted = append(deleted, oldPath)
		}
		path = unquoteGitPath(path)
		if strings.Contains(status, "D") {
			deleted = append(deleted, path)
		} else {
			changed = append(changed, path)
		}
	}
	return changed, deleted, nil
}

// ParseNameStatus parses `git diff --name-status` output into changed and
// deleted path lists, folding renames into a deletion of the old path plus a
// change of the new one.
func ParseNameStatus(raw string) (changed, deleted []string) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		switch {
		case strings.HasPrefix(status, "D"):
			deleted = append(deleted, fields[1])
		case strings.HasPrefix(status, "R") && len(fields) >= 3:
			deleted = append(deleted, fields[1])
			changed = append(changed, fields[2])
		default:
			changed = append(changed, fields[1])
		}
	}
	return changed, deleted
}

func unquoteGitPath(p string) string {
	if len(p) >= 2 && strings.HasPrefix(p, `"`) && strings.HasSuffix(p, `"`) {
		return strings.Trim(p, `"`)
	}
	return p
}
