package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestCollectDiffEmptyWorktree(t *testing.T) {
	ctx := context.Background()
	tree, cleanup := createTestWorktree(t, ctx, lens.Defensive)
	defer cleanup()

	diff, err := CollectDiff(ctx, tree)
	if err != nil {
		t.Fatalf("CollectDiff() returned error: %v", err)
	}

	if !diff.Empty {
		t.Fatal("Empty = false, want true")
	}
	if diff.Text != "" {
		t.Fatalf("Text = %q, want empty", diff.Text)
	}
	if len(diff.ChangedFiles) != 0 {
		t.Fatalf("ChangedFiles = %#v, want empty", diff.ChangedFiles)
	}
}

func TestCollectDiffModifiedTrackedFiles(t *testing.T) {
	ctx := context.Background()
	tree, cleanup := createTestWorktree(t, ctx, lens.Minimalist)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tree.Path, "app.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	diff, err := CollectDiff(ctx, tree)
	if err != nil {
		t.Fatalf("CollectDiff() returned error: %v", err)
	}

	if diff.Empty {
		t.Fatal("Empty = true, want false")
	}
	if len(diff.ChangedFiles) != 1 || diff.ChangedFiles[0] != "app.txt" {
		t.Fatalf("ChangedFiles = %#v, want app.txt", diff.ChangedFiles)
	}
	if !strings.Contains(diff.Text, "diff --git a/app.txt b/app.txt") {
		t.Fatalf("Text does not contain app.txt diff:\n%s", diff.Text)
	}
	if !strings.Contains(diff.Text, "+world") {
		t.Fatalf("Text does not contain added line:\n%s", diff.Text)
	}
}

func TestCollectDiffIncludesUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	tree, cleanup := createTestWorktree(t, ctx, lens.Performance)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tree.Path, "scratch.txt"), []byte("temporary\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	diff, err := CollectDiff(ctx, tree)
	if err != nil {
		t.Fatalf("CollectDiff() returned error: %v", err)
	}

	if diff.Empty {
		t.Fatal("Empty = true, want false for untracked file")
	}
	if len(diff.ChangedFiles) != 1 || diff.ChangedFiles[0] != "scratch.txt" {
		t.Fatalf("ChangedFiles = %#v, want scratch.txt", diff.ChangedFiles)
	}
	if !strings.Contains(diff.Text, "diff --git a/scratch.txt b/scratch.txt") {
		t.Fatalf("Text does not contain scratch.txt diff:\n%s", diff.Text)
	}
	if !strings.Contains(diff.Text, "+temporary") {
		t.Fatalf("Text does not contain untracked file content:\n%s", diff.Text)
	}
}

func TestCollectDiffRemovesUntrackedCodexArtifact(t *testing.T) {
	ctx := context.Background()
	tree, cleanup := createTestWorktree(t, ctx, lens.Architect)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tree.Path, ".codex"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(.codex) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree.Path, "app.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.txt) returned error: %v", err)
	}

	diff, err := CollectDiff(ctx, tree)
	if err != nil {
		t.Fatalf("CollectDiff() returned error: %v", err)
	}

	if len(diff.ChangedFiles) != 1 || diff.ChangedFiles[0] != "app.txt" {
		t.Fatalf("ChangedFiles = %#v, want app.txt", diff.ChangedFiles)
	}
	if strings.Contains(diff.Text, ".codex") {
		t.Fatalf("Text contains generated .codex artifact:\n%s", diff.Text)
	}
	if _, err := os.Stat(filepath.Join(tree.Path, ".codex")); !os.IsNotExist(err) {
		t.Fatalf(".codex should have been removed, stat err = %v", err)
	}
}

func TestApplyPatchAppliesDiffToWorktree(t *testing.T) {
	ctx := context.Background()
	tree, cleanup := createTestWorktree(t, ctx, lens.Defensive)
	defer cleanup()

	patch := strings.Join([]string{
		"diff --git a/app.txt b/app.txt",
		"index ce01362..94954ab 100644",
		"--- a/app.txt",
		"+++ b/app.txt",
		"@@ -1 +1,2 @@",
		" hello",
		"+world",
		"",
	}, "\n")

	if err := ApplyPatch(ctx, tree, patch); err != nil {
		t.Fatalf("ApplyPatch() returned error: %v", err)
	}

	payload, err := os.ReadFile(filepath.Join(tree.Path, "app.txt"))
	if err != nil {
		t.Fatalf("ReadFile(app.txt) returned error: %v", err)
	}
	if string(payload) != "hello\nworld\n" {
		t.Fatalf("app.txt = %q, want patched content", string(payload))
	}
}

func TestCollectDiffRequiresWorktreePath(t *testing.T) {
	_, err := CollectDiff(context.Background(), Tree{})
	if err == nil {
		t.Fatal("CollectDiff() returned nil error")
	}
}

func TestParseChangedFiles(t *testing.T) {
	got := parseChangedFiles("\n app.txt\n\ninternal/foo.go\n")
	want := []string{"app.txt", "internal/foo.go"}

	if len(got) != len(want) {
		t.Fatalf("parseChangedFiles() returned %d files, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseChangedFiles()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func createTestWorktree(t *testing.T, ctx context.Context, id lens.ID) (Tree, func()) {
	t.Helper()

	repoRoot, baseCommit := initRepo(t, ctx)
	rootDir := filepath.Join(t.TempDir(), "worktrees")
	manager, err := NewManager(Options{
		RepoRoot:   repoRoot,
		BaseCommit: baseCommit,
		RootDir:    rootDir,
	})
	if err != nil {
		t.Fatalf("NewManager() returned error: %v", err)
	}

	definition, ok := lens.Lookup(id)
	if !ok {
		t.Fatalf("lens.Lookup(%q) returned false", id)
	}
	tree, err := manager.Create(ctx, definition)
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	return tree, func() {
		if err := manager.Cleanup(context.Background()); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	}
}
