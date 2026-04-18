package repo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Metadata struct {
	Root         string   `json:"root"`
	BaseBranch   string   `json:"base_branch"`
	BaseCommit   string   `json:"base_commit"`
	Dirty        bool     `json:"dirty"`
	DirtySummary []string `json:"dirty_summary,omitempty"`
}

// Inspect captures the repository root, current branch, commit, and dirty state.
func Inspect(path string) (Metadata, error) {
	root, err := git(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return Metadata{}, fmt.Errorf("resolve repo root: %w", err)
	}

	root = filepath.Clean(root)

	branch, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Metadata{}, fmt.Errorf("detect branch: %w", err)
	}

	commit, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return Metadata{}, fmt.Errorf("detect commit: %w", err)
	}

	statusLines, err := gitLines(root, "status", "--short")
	if err != nil {
		return Metadata{}, fmt.Errorf("detect dirty state: %w", err)
	}

	return Metadata{
		Root:         root,
		BaseBranch:   branch,
		BaseCommit:   commit,
		Dirty:        len(statusLines) > 0,
		DirtySummary: statusLines,
	}, nil
}

// git runs a git command in the given directory and returns trimmed stdout.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// gitLines splits git output into non-empty trimmed lines.
func gitLines(dir string, args ...string) ([]string, error) {
	out, err := git(dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered, nil
}

// EnsureDir creates the output directory tree when it does not already exist.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
