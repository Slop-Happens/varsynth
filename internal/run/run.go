package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Slop-Happens/varsynth/internal/agent"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/report"
	"github.com/Slop-Happens/varsynth/internal/validation"
	"github.com/Slop-Happens/varsynth/internal/worktree"
)

type Options struct {
	RunID                 string
	RepoRoot              string
	BaseCommit            string
	TestCommand           string
	OutDir                string
	WorktreeRoot          string
	PreserveWorktrees     bool
	ValidationTimeout     time.Duration
	MaxValidationLogBytes int
	Agent                 agent.Runner
}

type Result struct {
	RunID        string
	OutDir       string
	WorktreeRoot string
	ReportPath   string
	Candidates   []CandidateResult
	CleanupError string
}

type CandidateResult struct {
	LensID       lens.ID
	ArtifactPath string
	Artifact     candidate.Artifact
	Error        string
}

func Execute(ctx context.Context, opts Options) (Result, error) {
	if strings.TrimSpace(opts.RunID) == "" {
		return Result{}, fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return Result{}, fmt.Errorf("output directory is required")
	}

	manager, err := worktree.NewManager(worktree.Options{
		RepoRoot:   opts.RepoRoot,
		BaseCommit: opts.BaseCommit,
		RootDir:    opts.WorktreeRoot,
		Preserve:   opts.PreserveWorktrees,
	})
	if err != nil {
		return Result{}, err
	}

	runner := opts.Agent
	if runner == nil {
		runner = agent.Stub{}
	}

	result := Result{
		RunID:        opts.RunID,
		OutDir:       opts.OutDir,
		WorktreeRoot: manager.RootDir(),
		Candidates:   make([]CandidateResult, 0, len(lens.All())),
	}

	var writeErrors []error
	for _, definition := range lens.All() {
		result.Candidates = append(result.Candidates, executeCandidate(ctx, opts, manager, runner, definition))
		last := result.Candidates[len(result.Candidates)-1]
		if last.Error != "" && last.ArtifactPath == "" {
			writeErrors = append(writeErrors, fmt.Errorf("%s: %s", definition.ID, last.Error))
		}
	}

	if err := manager.Cleanup(ctx); err != nil {
		result.CleanupError = err.Error()
		writeErrors = append(writeErrors, err)
	}

	reportPath, err := report.Write(opts.OutDir, buildReport(opts, result))
	if err != nil {
		writeErrors = append(writeErrors, err)
	} else {
		result.ReportPath = reportPath
	}

	return result, joinErrors(writeErrors)
}

func executeCandidate(ctx context.Context, opts Options, manager *worktree.Manager, runner agent.Runner, definition lens.Definition) CandidateResult {
	artifact := candidate.New(opts.RunID, definition, "")
	candidateResult := CandidateResult{
		LensID:   definition.ID,
		Artifact: artifact,
	}

	tree, err := manager.Create(ctx, definition)
	if err != nil {
		artifact.MarkFailed(err)
		return writeCandidate(opts.OutDir, artifact, candidateResult)
	}
	artifact.WorktreePath = tree.Path

	agentResult, err := runner.Run(ctx, agent.Input{
		RunID:        opts.RunID,
		Lens:         definition,
		WorktreePath: tree.Path,
	})
	if err != nil {
		artifact.MarkFailed(err)
		return writeCandidate(opts.OutDir, artifact, candidateResult)
	}
	artifact.Rationale = agentResult.Rationale
	artifact.RootCause = agentResult.RootCause
	artifact.MarkAgentNoop()

	diff, err := worktree.CollectDiff(ctx, tree)
	if err != nil {
		artifact.MarkFailed(err)
		return writeCandidate(opts.OutDir, artifact, candidateResult)
	}
	artifact.SetDiff(diff.ChangedFiles, diff.Text)

	validationResult := validation.Run(ctx, validation.Options{
		Command:     opts.TestCommand,
		WorkDir:     tree.Path,
		Timeout:     opts.ValidationTimeout,
		MaxLogBytes: opts.MaxValidationLogBytes,
	})
	artifact.SetValidation(validationResult)

	return writeCandidate(opts.OutDir, artifact, candidateResult)
}

func writeCandidate(outDir string, artifact candidate.Artifact, result CandidateResult) CandidateResult {
	path, err := candidate.Write(outDir, artifact)
	if err != nil {
		result.Error = err.Error()
		result.Artifact = artifact
		return result
	}
	result.ArtifactPath = path
	result.Artifact = artifact
	return result
}

func buildReport(opts Options, result Result) report.Summary {
	summary := report.Summary{
		RunID:        opts.RunID,
		RepoRoot:     opts.RepoRoot,
		BaseCommit:   opts.BaseCommit,
		TestCommand:  opts.TestCommand,
		OutDir:       opts.OutDir,
		WorktreeRoot: result.WorktreeRoot,
		CleanupError: result.CleanupError,
		Candidates:   make([]report.CandidateSummary, 0, len(result.Candidates)),
	}
	for _, candidateResult := range result.Candidates {
		summary.Candidates = append(summary.Candidates, report.FromArtifact(
			candidateResult.ArtifactPath,
			candidateResult.Artifact,
			candidateResult.Error,
		))
	}
	return summary
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	var builder strings.Builder
	for i, err := range errs {
		if i > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString(err.Error())
	}
	return errors.New(builder.String())
}
