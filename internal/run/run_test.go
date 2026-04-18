package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Slop-Happens/varsynth/internal/agent"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
	reportpkg "github.com/Slop-Happens/varsynth/internal/report"
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
	if result.ReportPath != reportpkg.Path(outDir) {
		t.Fatalf("ReportPath = %q, want %q", result.ReportPath, reportpkg.Path(outDir))
	}

	report := readReport(t, result.ReportPath)
	if report.RunID != "run-1" {
		t.Fatalf("report RunID = %q, want run-1", report.RunID)
	}
	if report.RepoRoot != repoRoot {
		t.Fatalf("report RepoRoot = %q, want %q", report.RepoRoot, repoRoot)
	}
	if report.BaseCommit != baseCommit {
		t.Fatalf("report BaseCommit = %q, want %q", report.BaseCommit, baseCommit)
	}
	if report.TestCommand != "test -f app.txt" {
		t.Fatalf("report TestCommand = %q, want test -f app.txt", report.TestCommand)
	}
	if len(report.Candidates) != len(definitions) {
		t.Fatalf("report has %d candidates, want %d", len(report.Candidates), len(definitions))
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

func TestExecuteRunsAgentsConcurrently(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	probe := &concurrencyAgent{delay: 100 * time.Millisecond}

	result, err := Execute(ctx, Options{
		RunID:        "run-parallel",
		RepoRoot:     repoRoot,
		BaseCommit:   baseCommit,
		TestCommand:  "true",
		OutDir:       t.TempDir(),
		WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		Agent:        probe,
	})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(result.Candidates) != len(lens.All()) {
		t.Fatalf("Execute() returned %d candidates, want %d", len(result.Candidates), len(lens.All()))
	}
	if probe.MaxConcurrent() < 2 {
		t.Fatalf("max concurrent agent calls = %d, want at least 2", probe.MaxConcurrent())
	}
}

func TestExecuteWaitsForAllConcurrentAgents(t *testing.T) {
	ctx := context.Background()
	repoRoot, baseCommit := initRepo(t, ctx)
	gate := newBlockingAgent(len(lens.All()))
	done := make(chan executeResult, 1)

	go func() {
		result, err := Execute(ctx, Options{
			RunID:        "run-wait",
			RepoRoot:     repoRoot,
			BaseCommit:   baseCommit,
			TestCommand:  "true",
			OutDir:       t.TempDir(),
			WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
			Agent:        gate,
		})
		done <- executeResult{result: result, err: err}
	}()

	gate.waitForAllStarted(t)

	select {
	case completed := <-done:
		t.Fatalf("Execute() returned before agents were released: result=%#v err=%v", completed.result, completed.err)
	case <-time.After(25 * time.Millisecond):
	}

	gate.release()

	select {
	case completed := <-done:
		if completed.err != nil {
			t.Fatalf("Execute() returned error: %v", completed.err)
		}
		if len(completed.result.Candidates) != len(lens.All()) {
			t.Fatalf("Execute() returned %d candidates, want %d", len(completed.result.Candidates), len(lens.All()))
		}
		for _, candidateResult := range completed.result.Candidates {
			if candidateResult.Artifact.Status != candidate.StatusValidationPassed {
				t.Fatalf("%s Status = %q, want %q", candidateResult.LensID, candidateResult.Artifact.Status, candidate.StatusValidationPassed)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute() did not return after agents were released")
	}

	if gate.Completed() != len(lens.All()) {
		t.Fatalf("completed agent calls = %d, want %d", gate.Completed(), len(lens.All()))
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

	report := readReport(t, result.ReportPath)
	var minimalistSummary reportpkg.CandidateSummary
	var performanceSummary reportpkg.CandidateSummary
	for _, summary := range report.Candidates {
		switch summary.LensID {
		case lens.Minimalist:
			minimalistSummary = summary
		case lens.Performance:
			performanceSummary = summary
		}
	}
	if minimalistSummary.Status != candidate.StatusFailed {
		t.Fatalf("minimalist report Status = %q, want %q", minimalistSummary.Status, candidate.StatusFailed)
	}
	if minimalistSummary.Error != "minimalist failed" {
		t.Fatalf("minimalist report Error = %q, want minimalist failed", minimalistSummary.Error)
	}
	if performanceSummary.ChangedFileCount != 1 {
		t.Fatalf("performance report ChangedFileCount = %d, want 1", performanceSummary.ChangedFileCount)
	}
	if performanceSummary.EmptyDiff {
		t.Fatal("performance report EmptyDiff = true, want false")
	}
	if performanceSummary.DiffBytes == 0 {
		t.Fatal("performance report DiffBytes = 0, want non-zero")
	}
	if !performanceSummary.RationalePresent {
		t.Fatal("performance report RationalePresent = false, want true")
	}
	if !performanceSummary.RootCausePresent {
		t.Fatal("performance report RootCausePresent = false, want true")
	}
	if performanceSummary.ValidationStatus != candidate.ValidationPassed {
		t.Fatalf("performance report ValidationStatus = %q, want %q", performanceSummary.ValidationStatus, candidate.ValidationPassed)
	}
	if performanceSummary.ValidationExit == nil || *performanceSummary.ValidationExit != 0 {
		t.Fatalf("performance report ValidationExit = %v, want 0", performanceSummary.ValidationExit)
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

type concurrencyAgent struct {
	mu      sync.Mutex
	current int
	max     int
	delay   time.Duration
}

func (probe *concurrencyAgent) Run(ctx context.Context, input agent.Input) (agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return agent.Result{}, err
	}

	probe.mu.Lock()
	probe.current++
	if probe.current > probe.max {
		probe.max = probe.current
	}
	probe.mu.Unlock()
	defer func() {
		probe.mu.Lock()
		probe.current--
		probe.mu.Unlock()
	}()

	select {
	case <-time.After(probe.delay):
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	}

	return agent.Result{
		Rationale: "concurrency probe",
		RootCause: "concurrency probe",
	}, nil
}

func (probe *concurrencyAgent) MaxConcurrent() int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.max
}

type executeResult struct {
	result Result
	err    error
}

type blockingAgent struct {
	expected  int
	started   chan lens.ID
	released  chan struct{}
	completed int
	mu        sync.Mutex
}

func newBlockingAgent(expected int) *blockingAgent {
	return &blockingAgent{
		expected: expected,
		started:  make(chan lens.ID, expected),
		released: make(chan struct{}),
	}
}

func (gate *blockingAgent) Run(ctx context.Context, input agent.Input) (agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return agent.Result{}, err
	}

	gate.started <- input.Lens.ID

	select {
	case <-gate.released:
	case <-ctx.Done():
		return agent.Result{}, ctx.Err()
	}

	gate.mu.Lock()
	gate.completed++
	gate.mu.Unlock()

	return agent.Result{
		Rationale: "blocking probe",
		RootCause: "blocking probe",
	}, nil
}

func (gate *blockingAgent) waitForAllStarted(t *testing.T) {
	t.Helper()

	seen := map[lens.ID]bool{}
	for len(seen) < gate.expected {
		select {
		case id := <-gate.started:
			seen[id] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("only saw %d/%d agent calls start", len(seen), gate.expected)
		}
	}
}

func (gate *blockingAgent) release() {
	close(gate.released)
}

func (gate *blockingAgent) Completed() int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.completed
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

func readReport(t *testing.T, path string) reportpkg.Summary {
	t.Helper()

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", path, err)
	}

	var summary reportpkg.Summary
	if err := json.Unmarshal(payload, &summary); err != nil {
		t.Fatalf("Unmarshal(%s) returned error: %v", path, err)
	}
	return summary
}
