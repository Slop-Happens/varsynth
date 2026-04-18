package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/agent"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestExecuteCreatesCandidateArtifacts(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	outDir := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")

	result, err := Execute(ctx, Options{
		RunID:        "run-1",
		RepoRoot:     repoRoot,
		BaseCommit:   baseCommit,
		TestCommand:  "test -f app.txt",
		OutDir:       outDir,
		WorktreeRoot: worktreeRoot,
	})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	definitions := lens.All()
	if len(result.Candidates) != len(definitions) {
		t.Fatalf("Execute() returned %d candidates, want %d", len(result.Candidates), len(definitions))
	}

	for _, definition := range definitions {
		path := candidate.Path(outDir, definition.ID)
		artifact := readArtifact(t, path)

		if artifact.RunID != "run-1" {
			t.Fatalf("%s RunID = %q, want run-1", definition.ID, artifact.RunID)
		}
		if artifact.Lens.ID != definition.ID {
			t.Fatalf("%s Lens.ID = %q, want %q", definition.ID, artifact.Lens.ID, definition.ID)
		}
		if artifact.Status != candidate.StatusValidationPassed {
			t.Fatalf("%s Status = %q, want %q", definition.ID, artifact.Status, candidate.StatusValidationPassed)
		}
		if artifact.WorktreePath == "" {
			t.Fatalf("%s WorktreePath is empty", definition.ID)
		}
		if _, err := os.Stat(artifact.WorktreePath); !os.IsNotExist(err) {
			t.Fatalf("%s worktree path still exists after cleanup: %v", definition.ID, err)
		}
		if !artifact.EmptyDiff {
			t.Fatalf("%s EmptyDiff = false, want true", definition.ID)
		}
		if len(artifact.ChangedFiles) != 0 {
			t.Fatalf("%s ChangedFiles = %#v, want empty", definition.ID, artifact.ChangedFiles)
		}
		if artifact.Diff != "" {
			t.Fatalf("%s Diff = %q, want empty", definition.ID, artifact.Diff)
		}
		if artifact.Validation.Status != candidate.ValidationPassed {
			t.Fatalf("%s Validation.Status = %q, want %q", definition.ID, artifact.Validation.Status, candidate.ValidationPassed)
		}
	}
}

func TestExecutePreservesWorktrees(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	outDir := t.TempDir()

	result, err := Execute(ctx, Options{
		RunID:             "run-preserve",
		RepoRoot:          repoRoot,
		BaseCommit:        baseCommit,
		TestCommand:       "true",
		OutDir:            outDir,
		WorktreeRoot:      filepath.Join(t.TempDir(), "worktrees"),
		PreserveWorktrees: true,
	})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	t.Cleanup(func() {
		for _, candidateResult := range result.Candidates {
			if candidateResult.Artifact.WorktreePath != "" {
				runGit(t, context.Background(), repoRoot, "worktree", "remove", "--force", candidateResult.Artifact.WorktreePath)
			}
		}
	})

	for _, candidateResult := range result.Candidates {
		if _, err := os.Stat(candidateResult.Artifact.WorktreePath); err != nil {
			t.Fatalf("%s preserved worktree missing: %v", candidateResult.LensID, err)
		}
	}
}

func TestExecuteIsolatesCandidateAgentFailure(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	outDir := t.TempDir()

	result, err := Execute(ctx, Options{
		RunID:        "run-failure",
		RepoRoot:     repoRoot,
		BaseCommit:   baseCommit,
		TestCommand:  "true",
		OutDir:       outDir,
		WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		Agent: scriptedAgent{
			run: func(input agent.Input) (agent.Result, error) {
				switch input.Lens.ID {
				case lens.Minimalist:
					return agent.Result{}, fmt.Errorf("minimalist failed")
				case lens.Performance:
					path := filepath.Join(input.WorktreePath, "candidate.txt")
					if err := os.WriteFile(path, []byte("new file\n"), 0o644); err != nil {
						return agent.Result{}, err
					}
					return agent.Result{
						Rationale: "created candidate.txt",
						RootCause: "performance root cause placeholder",
					}, nil
				default:
					return agent.Result{
						Rationale: "no changes",
						RootCause: "root cause placeholder",
					}, nil
				}
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(result.Candidates) != len(lens.All()) {
		t.Fatalf("Execute() returned %d candidates, want %d", len(result.Candidates), len(lens.All()))
	}

	minimalist := readArtifact(t, candidate.Path(outDir, lens.Minimalist))
	if minimalist.Status != candidate.StatusFailed {
		t.Fatalf("minimalist Status = %q, want %q", minimalist.Status, candidate.StatusFailed)
	}
	if minimalist.Error != "minimalist failed" {
		t.Fatalf("minimalist Error = %q, want minimalist failed", minimalist.Error)
	}
	if minimalist.Validation.Status != candidate.ValidationNotRun {
		t.Fatalf("minimalist Validation.Status = %q, want %q", minimalist.Validation.Status, candidate.ValidationNotRun)
	}

	performance := readArtifact(t, candidate.Path(outDir, lens.Performance))
	if performance.Status != candidate.StatusValidationPassed {
		t.Fatalf("performance Status = %q, want %q", performance.Status, candidate.StatusValidationPassed)
	}
	if performance.EmptyDiff {
		t.Fatal("performance EmptyDiff = true, want false")
	}
	if len(performance.ChangedFiles) != 1 || performance.ChangedFiles[0] != "candidate.txt" {
		t.Fatalf("performance ChangedFiles = %#v, want candidate.txt", performance.ChangedFiles)
	}
	if !strings.Contains(performance.Diff, "diff --git a/candidate.txt b/candidate.txt") {
		t.Fatalf("performance Diff does not include new file:\n%s", performance.Diff)
	}

	defensive := readArtifact(t, candidate.Path(outDir, lens.Defensive))
	if defensive.Status != candidate.StatusValidationPassed {
		t.Fatalf("defensive Status = %q, want %q", defensive.Status, candidate.StatusValidationPassed)
	}
}

func TestExecuteRequiresRunIDAndOutDir(t *testing.T) {
	if _, err := Execute(context.Background(), Options{OutDir: t.TempDir()}); err == nil {
		t.Fatal("Execute() with empty run id returned nil error")
	}
	if _, err := Execute(context.Background(), Options{RunID: "run"}); err == nil {
		t.Fatal("Execute() with empty out dir returned nil error")
	}
}

type scriptedAgent struct {
	run func(input agent.Input) (agent.Result, error)
}

func (script scriptedAgent) Run(ctx context.Context, input agent.Input) (agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return agent.Result{}, err
	}
	return script.run(input)
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

func readArtifact(t *testing.T, path string) candidate.Artifact {
	t.Helper()

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", path, err)
	}

	var artifact candidate.Artifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("Unmarshal(%s) returned error: %v", path, err)
	}
	return artifact
}
