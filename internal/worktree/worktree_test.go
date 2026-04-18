package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestNewManagerRequiresRepoRootAndBaseCommit(t *testing.T) {
	if _, err := NewManager(Options{BaseCommit: "abc123"}); err == nil {
		t.Fatal("NewManager() with empty repo root returned nil error")
	}

	if _, err := NewManager(Options{RepoRoot: t.TempDir()}); err == nil {
		t.Fatal("NewManager() with empty base commit returned nil error")
	}
}

func TestManagerCreateAndCleanup(t *testing.T) {
	ctx := context.Background()
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
	t.Cleanup(func() {
		if err := manager.Cleanup(context.Background()); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})

	defensive, ok := lens.Lookup(lens.Defensive)
	if !ok {
		t.Fatal("lens.Lookup(Defensive) returned false")
	}

	tree, err := manager.Create(ctx, defensive)
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	if tree.LensID != lens.Defensive {
		t.Fatalf("LensID = %q, want %q", tree.LensID, lens.Defensive)
	}
	if tree.Path != filepath.Join(rootDir, "defensive") {
		t.Fatalf("Path = %q, want %q", tree.Path, filepath.Join(rootDir, "defensive"))
	}
	if tree.BaseCommit != baseCommit {
		t.Fatalf("BaseCommit = %q, want %q", tree.BaseCommit, baseCommit)
	}
	if _, err := os.Stat(filepath.Join(tree.Path, "app.txt")); err != nil {
		t.Fatalf("worktree file missing: %v", err)
	}

	gotHead := strings.TrimSpace(runGit(t, ctx, tree.Path, "rev-parse", "HEAD"))
	if gotHead != baseCommit {
		t.Fatalf("worktree HEAD = %q, want %q", gotHead, baseCommit)
	}

	if err := manager.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup() returned error: %v", err)
	}
	if _, err := os.Stat(tree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after cleanup: %v", err)
	}
}

func TestManagerPreserveSkipsCleanup(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	rootDir := filepath.Join(t.TempDir(), "worktrees")

	manager, err := NewManager(Options{
		RepoRoot:   repoRoot,
		BaseCommit: baseCommit,
		RootDir:    rootDir,
		Preserve:   true,
	})
	if err != nil {
		t.Fatalf("NewManager() returned error: %v", err)
	}

	definition, ok := lens.Lookup(lens.Minimalist)
	if !ok {
		t.Fatal("lens.Lookup(Minimalist) returned false")
	}
	tree, err := manager.Create(ctx, definition)
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	t.Cleanup(func() {
		cleanupManager, err := NewManager(Options{
			RepoRoot:   repoRoot,
			BaseCommit: baseCommit,
			RootDir:    rootDir,
		})
		if err != nil {
			t.Fatalf("NewManager() for forced cleanup returned error: %v", err)
		}
		cleanupManager.created = []Tree{tree}
		if err := cleanupManager.Cleanup(context.Background()); err != nil {
			t.Fatalf("forced cleanup failed: %v", err)
		}
	})

	if err := manager.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup() returned error: %v", err)
	}
	if _, err := os.Stat(tree.Path); err != nil {
		t.Fatalf("preserved worktree path missing: %v", err)
	}
}

func TestCreatedReturnsCopy(t *testing.T) {
	manager := &Manager{
		created: []Tree{{LensID: lens.Architect, Path: "/tmp/architect"}},
	}

	created := manager.Created()
	created[0].Path = "/tmp/mutated"

	if manager.created[0].Path != "/tmp/architect" {
		t.Fatal("Created() returned manager storage instead of a copy")
	}
}

func TestCreateRejectsMissingLensID(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	manager, err := NewManager(Options{
		RepoRoot:   repoRoot,
		BaseCommit: baseCommit,
		RootDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager() returned error: %v", err)
	}

	if _, err := manager.Create(ctx, lens.Definition{}); err == nil {
		t.Fatal("Create() returned nil error")
	}
}

func initRepo(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()

	repoRoot := t.TempDir()
	runGit(t, ctx, repoRoot, "init")
	runGit(t, ctx, repoRoot, "config", "user.name", "Varsynth Test")
	runGit(t, ctx, repoRoot, "config", "user.email", "varsynth@example.test")

	if err := os.WriteFile(filepath.Join(repoRoot, "app.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	runGit(t, ctx, repoRoot, "add", "app.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "initial")

	return repoRoot, strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))
}

func runGit(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
