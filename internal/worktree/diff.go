package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Diff struct {
	ChangedFiles []string `json:"changed_files"`
	Text         string   `json:"text"`
	Empty        bool     `json:"empty"`
}

// CollectDiff marks untracked files as intent-to-add, then returns the worktree diff.
func CollectDiff(ctx context.Context, tree Tree) (Diff, error) {
	if strings.TrimSpace(tree.Path) == "" {
		return Diff{}, fmt.Errorf("worktree path is required")
	}

	if err := removeUntrackedCodexArtifact(ctx, tree.Path); err != nil {
		return Diff{}, err
	}
	if err := markUntrackedIntentToAdd(ctx, tree.Path); err != nil {
		return Diff{}, err
	}

	changedOutput, err := git(ctx, tree.Path, "diff", "--name-only")
	if err != nil {
		return Diff{}, fmt.Errorf("collect changed files: %w", err)
	}

	diffOutput, err := git(ctx, tree.Path, "diff", "--no-color")
	if err != nil {
		return Diff{}, fmt.Errorf("collect diff: %w", err)
	}

	changedFiles := parseChangedFiles(string(changedOutput))
	diffText := string(diffOutput)

	return Diff{
		ChangedFiles: changedFiles,
		Text:         diffText,
		Empty:        diffText == "",
	}, nil
}

func removeUntrackedCodexArtifact(ctx context.Context, path string) error {
	codexPath := filepath.Join(path, ".codex")
	if _, err := os.Lstat(codexPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect codex artifact: %w", err)
	}

	if _, err := git(ctx, path, "ls-files", "--error-unmatch", "--", ".codex"); err == nil {
		return nil
	}
	if err := os.RemoveAll(codexPath); err != nil {
		return fmt.Errorf("remove untracked codex artifact: %w", err)
	}
	return nil
}

func markUntrackedIntentToAdd(ctx context.Context, path string) error {
	output, err := git(ctx, path, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return fmt.Errorf("collect untracked files: %w", err)
	}

	untrackedFiles := parseChangedFiles(string(output))
	if len(untrackedFiles) == 0 {
		return nil
	}

	args := append([]string{"add", "-N", "--"}, untrackedFiles...)
	if _, err := git(ctx, path, args...); err != nil {
		return fmt.Errorf("mark untracked files intent-to-add: %w", err)
	}
	return nil
}

func parseChangedFiles(output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return []string{}
	}

	lines := strings.Split(output, "\n")
	changedFiles := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			changedFiles = append(changedFiles, line)
		}
	}
	return changedFiles
}
