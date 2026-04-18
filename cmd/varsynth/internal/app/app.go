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
	"github.com/Slop-Happens/varsynth/internal/agent"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/prompt"
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

	fmt.Fprintf(stdout, "Agent: %s\n", cfg.AgentMode)
	fmt.Fprintf(stdout, "Prompt artifacts: %d\n", countPromptArtifacts(runResult.Candidates))
	fmt.Fprintf(stdout, "Candidate artifacts: %d\n", countWrittenArtifacts(runResult.Candidates))
	fmt.Fprintf(stdout, "Validation: passed=%d failed=%d timed_out=%d not_run=%d\n",
		countValidationStatus(runResult.Candidates, candidate.ValidationPassed),
		countValidationStatus(runResult.Candidates, candidate.ValidationFailed),
		countValidationStatus(runResult.Candidates, candidate.ValidationTimedOut),
		countValidationStatus(runResult.Candidates, candidate.ValidationNotRun),
	)
	fmt.Fprintf(stdout, "Report: %s\n", runResult.ReportPath)
	fmt.Fprintf(stdout, "Run events: %s\n", runResult.RunEventsPath)
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
		AgentConcurrency:  cfg.AgentConcurrency,
		AgentRetries:      cfg.AgentRetries,
		AgentRetryDelay:   cfg.AgentRetryDelay,
		PromptContext:     promptContextFromBundle(bundle),
		Agent:             agentRunnerFromConfig(cfg),
	}
}

func promptContextFromBundle(bundle ctxbundle.Bundle) prompt.Context {
	ctx := prompt.Context{
		RunID:        bundle.RunID,
		RepoRoot:     bundle.RepoRoot,
		BaseBranch:   bundle.BaseBranch,
		BaseCommit:   bundle.BaseCommit,
		TestCommand:  bundle.TestCommand,
		Dirty:        bundle.DirtyState.Dirty,
		DirtySummary: append([]string(nil), bundle.DirtyState.Summary...),
		Issue: prompt.Issue{
			ID:          bundle.Issue.ID,
			Title:       bundle.Issue.Title,
			Message:     bundle.Issue.Message,
			Service:     bundle.Issue.Service,
			Environment: bundle.Issue.Environment,
		},
		StackFrames: make([]prompt.StackFrame, 0, len(bundle.StackFrames)),
		Snippets:    make([]prompt.Snippet, 0, len(bundle.Snippets)),
	}
	for _, frame := range bundle.StackFrames {
		ctx.StackFrames = append(ctx.StackFrames, prompt.StackFrame{
			Index:         frame.Index,
			File:          frame.File,
			Line:          frame.Line,
			Function:      frame.Function,
			Module:        frame.Module,
			InApp:         frame.InApp,
			ResolvedPath:  frame.ResolvedPath,
			Status:        frame.Status,
			SnippetID:     frame.SnippetID,
			StatusMessage: frame.StatusMessage,
		})
	}
	for _, snippet := range bundle.Snippets {
		ctx.Snippets = append(ctx.Snippets, prompt.Snippet{
			ID:          snippet.ID,
			File:        snippet.File,
			StartLine:   snippet.StartLine,
			EndLine:     snippet.EndLine,
			FocusLine:   snippet.FocusLine,
			SourceLines: append([]string(nil), snippet.SourceLines...),
		})
	}
	return ctx
}

func agentRunnerFromConfig(cfg config.Config) agent.Runner {
	if cfg.AgentMode != config.AgentCodex {
		return agent.Stub{}
	}
	return agent.BackendRunner{
		Backend: agent.CodexBackend{
			Command:  cfg.CodexCommand,
			Model:    cfg.CodexModel,
			FullAuto: cfg.CodexFullAuto,
			Timeout:  cfg.AgentTimeout,
		},
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

func countPromptArtifacts(results []runpkg.CandidateResult) int {
	count := 0
	for _, result := range results {
		if result.Artifact.PromptPath != "" {
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
