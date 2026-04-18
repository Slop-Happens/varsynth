package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/config"
	ctxbundle "github.com/Slop-Happens/varsynth/cmd/varsynth/internal/context"
	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/issue"
	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/repo"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	runpkg "github.com/Slop-Happens/varsynth/internal/run"
)

// Run executes the offline bootstrap pipeline from CLI input to context artifact.
func Run(args []string, stdout, stderr io.Writer) error {
	cfg, err := config.Parse(args, stderr)
	if err != nil {
		return err
	}

	meta, err := repo.Inspect(cfg.RepoPath)
	if err != nil {
		return err
	}

	iss, err := issue.Load(cfg.IssueFile)
	if err != nil {
		return err
	}

	bundle, err := ctxbundle.Build(cfg, meta, iss)
	if err != nil {
		return err
	}

	if err := repo.EnsureDir(cfg.OutDir); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	contextPath := filepath.Join(cfg.OutDir, "context.json")
	if err := ctxbundle.Write(contextPath, bundle); err != nil {
		return err
	}

	mappedCount := len(bundle.Snippets)
	fmt.Fprintf(stdout, "Run ID: %s\n", bundle.RunID)
	fmt.Fprintf(stdout, "Repository: %s\n", bundle.RepoRoot)
	fmt.Fprintf(stdout, "Base: %s @ %s\n", bundle.BaseBranch, bundle.BaseCommit)
	fmt.Fprintf(stdout, "Dirty: %t\n", bundle.DirtyState.Dirty)
	fmt.Fprintf(stdout, "Issue: %s (%s)\n", bundle.Issue.Title, bundle.Issue.ID)
	fmt.Fprintf(stdout, "Frames: %d total, %d mapped, %d unmapped\n", len(bundle.StackFrames), mappedCount, len(bundle.StackFrames)-mappedCount)
	fmt.Fprintf(stdout, "Artifact: %s\n", contextPath)
	if cfg.DryRun {
		fmt.Fprintln(stdout, "Dry run: downstream execution skipped")
		return nil
	}

	runResult, runErr := runpkg.Execute(context.Background(), runOptionsFromBundle(cfg, bundle))

	fmt.Fprintf(stdout, "Candidate artifacts: %d\n", countWrittenArtifacts(runResult.Candidates))
	fmt.Fprintf(stdout, "Validation: passed=%d failed=%d timed_out=%d not_run=%d\n",
		countValidationStatus(runResult.Candidates, candidate.ValidationPassed),
		countValidationStatus(runResult.Candidates, candidate.ValidationFailed),
		countValidationStatus(runResult.Candidates, candidate.ValidationTimedOut),
		countValidationStatus(runResult.Candidates, candidate.ValidationNotRun),
	)
	fmt.Fprintf(stdout, "Report: %s\n", runResult.ReportPath)
	if cfg.PreserveWorktrees {
		fmt.Fprintf(stdout, "Worktrees: preserved at %s\n", runResult.WorktreeRoot)
	} else {
		fmt.Fprintf(stdout, "Worktrees: cleaned up under %s\n", runResult.WorktreeRoot)
	}
	if runResult.CleanupError != "" {
		fmt.Fprintf(stdout, "Cleanup warning: %s\n", runResult.CleanupError)
	}

	return runErr
}

func runOptionsFromBundle(cfg config.Config, bundle ctxbundle.Bundle) runpkg.Options {
	return runpkg.Options{
		RunID:             bundle.RunID,
		RepoRoot:          bundle.RepoRoot,
		BaseCommit:        bundle.BaseCommit,
		TestCommand:       bundle.TestCommand,
		OutDir:            cfg.OutDir,
		WorktreeRoot:      filepath.Join(cfg.OutDir, "worktrees"),
		PreserveWorktrees: cfg.PreserveWorktrees,
	}
}

func countWrittenArtifacts(results []runpkg.CandidateResult) int {
	count := 0
	for _, result := range results {
		if result.ArtifactPath != "" {
			count++
		}
	}
	return count
}

func countValidationStatus(results []runpkg.CandidateResult, want candidate.ValidationStatus) int {
	count := 0
	for _, result := range results {
		if result.Artifact.Validation.Status == want {
			count++
		}
	}
	return count
}
