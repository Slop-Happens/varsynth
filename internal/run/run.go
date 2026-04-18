package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Slop-Happens/varsynth/internal/agent"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/evaluation"
	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/prompt"
	"github.com/Slop-Happens/varsynth/internal/report"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
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
	MaxAgentLogBytes      int
	AgentConcurrency      int
	AgentRetries          int
	AgentRetryDelay       time.Duration
	PromptContext         prompt.Context
	Agent                 agent.Runner
	SelectCandidate       string
	CriticMode            string
	FinalPatch            string
	Critic                evaluation.Critic
}

type Result struct {
	RunID               string
	OutDir              string
	WorktreeRoot        string
	ReportPath          string
	RunEventsPath       string
	EvaluationPath      string
	FinalPatchPath      string
	Candidates          []CandidateResult
	CleanupError        string
	Evaluation          evaluation.Result
	FinalImplementation *evaluation.FinalImplementationResult
}

type CandidateResult struct {
	LensID       lens.ID
	ArtifactPath string
	Artifact     candidate.Artifact
	Error        string
}

type preparedCandidate struct {
	Index      int
	Definition lens.Definition
	Tree       worktree.Tree
	Artifact   candidate.Artifact
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
		RunID:         opts.RunID,
		OutDir:        opts.OutDir,
		WorktreeRoot:  manager.RootDir(),
		RunEventsPath: RunEventsPath(opts.OutDir),
		Candidates:    make([]CandidateResult, len(lens.All())),
	}
	events := newEventRecorder(opts.OutDir, opts.RunID)
	_ = os.Remove(events.Path())

	var prepared []preparedCandidate
	for i, definition := range lens.All() {
		artifact := candidate.New(opts.RunID, definition, "")
		candidateResult := CandidateResult{
			LensID:   definition.ID,
			Artifact: artifact,
		}

		tree, err := manager.Create(ctx, definition)
		if err != nil {
			artifact.MarkFailedAt(candidate.FailureSetup, err)
			result.Candidates[i] = writeCandidate(opts.OutDir, artifact, candidateResult, events)
			continue
		}
		artifact.WorktreePath = tree.Path
		prepared = append(prepared, preparedCandidate{
			Index:      i,
			Definition: definition,
			Tree:       tree,
			Artifact:   artifact,
		})
	}

	executePreparedCandidates(ctx, opts, runner, prepared, result.Candidates, events)

	writeErrors := collectWriteErrors(result.Candidates)
	writeErrors = append(writeErrors, events.Errors()...)

	evaluationResult, err := evaluation.Evaluate(ctx, evaluation.Options{
		RunID:      opts.RunID,
		OutDir:     opts.OutDir,
		Selector:   opts.SelectCandidate,
		CriticMode: opts.CriticMode,
		FinalPatch: opts.FinalPatch,
		Critic:     opts.Critic,
	}, buildEvaluationInputs(result.Candidates))
	if err != nil {
		writeErrors = append(writeErrors, err)
	} else {
		result.Evaluation = evaluationResult
		result.EvaluationPath = evaluation.Path(opts.OutDir)
		result.FinalPatchPath = evaluationResult.FinalPatchPath
	}

	if err == nil && shouldRunFinalImplementation(opts, evaluationResult) {
		finalResult, finalErr := executeFinalImplementation(ctx, opts, runner, manager, evaluationResult, result.Candidates, events)
		result.FinalImplementation = &finalResult
		result.Evaluation.FinalImplementation = &finalResult
		if finalResult.FinalPatchPath != "" {
			result.FinalPatchPath = finalResult.FinalPatchPath
			result.Evaluation.FinalPatchPath = finalResult.FinalPatchPath
		}
		if finalResult.UsedFallback && evaluationResult.FinalPatchPath != "" {
			result.FinalPatchPath = evaluationResult.FinalPatchPath
		}
		if writeErr := evaluation.WriteResult(opts.OutDir, result.Evaluation); writeErr != nil {
			writeErrors = append(writeErrors, writeErr)
		}
		if finalErr != nil {
			writeErrors = append(writeErrors, finalErr)
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

func buildEvaluationInputs(results []CandidateResult) []evaluation.Input {
	inputs := make([]evaluation.Input, 0, len(results))
	for _, result := range results {
		inputs = append(inputs, evaluation.Input{
			ArtifactPath: result.ArtifactPath,
			Artifact:     result.Artifact,
			Error:        result.Error,
		})
	}
	return inputs
}

func shouldRunFinalImplementation(opts Options, result evaluation.Result) bool {
	if opts.CriticMode == evaluation.CriticOff || opts.CriticMode == "" {
		return false
	}
	return result.ViableCandidateFound && result.FinalPatchPath != ""
}

func executeFinalImplementation(ctx context.Context, opts Options, runner agent.Runner, manager *worktree.Manager, evalResult evaluation.Result, candidates []CandidateResult, events *eventRecorder) (evaluation.FinalImplementationResult, error) {
	final := evaluation.FinalImplementationResult{
		Attempted:      true,
		UsedFallback:   true,
		FinalPatchPath: evalResult.FinalPatchPath,
	}

	selected := findCandidate(candidates, evalResult.SelectedLensID)
	if selected == nil {
		final.Error = "selected candidate artifact not found"
		return final, nil
	}
	if strings.TrimSpace(selected.Artifact.Diff) == "" {
		final.Error = "selected candidate diff is empty"
		return final, nil
	}

	definition := lens.Definition{
		ID:          lens.ID("final"),
		Name:        "Final",
		Description: "Final implementation synthesized from selected candidates and critic guidance.",
	}
	tree, err := manager.Create(ctx, definition)
	if err != nil {
		final.Error = sanitize.Secrets(err.Error())
		return final, nil
	}
	final.WorktreePath = tree.Path

	if err := worktree.ApplyPatch(ctx, tree, selected.Artifact.Diff); err != nil {
		final.Error = sanitize.Secrets(err.Error())
		return final, nil
	}

	promptText := finalPrompt(opts, evalResult, *selected)
	promptPath := filepath.Join(opts.OutDir, "prompts", "final.md")
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		final.Error = sanitize.Secrets(err.Error())
		return final, nil
	}
	if err := os.WriteFile(promptPath, []byte(promptText), 0o644); err != nil {
		final.Error = sanitize.Secrets(err.Error())
		return final, nil
	}
	final.PromptPath = promptPath

	events.Emit(Event{Type: eventFinalAgentStarted, LensID: definition.ID})
	agentResult, agentMeta, err := runAgentWithRetry(ctx, opts, runner, definition, tree, prompt.Payload{
		RunID: opts.RunID,
		Lens:  definition,
		Text:  promptText,
	}, promptPath, make(chan struct{}, 1), events)
	final.AgentLogDir = agentMeta.LogDir
	if err != nil {
		final.Error = sanitize.Secrets(err.Error())
		events.Emit(Event{Type: eventFinalAgentFinished, LensID: definition.ID, Status: "failed", Error: err.Error()})
		return final, nil
	}
	events.Emit(Event{Type: eventFinalAgentFinished, LensID: definition.ID, Status: "success"})

	artifact := candidate.New(opts.RunID, definition, tree.Path)
	artifact.PromptPath = promptPath
	artifact.SetAgentResult(agentResult.Rationale, agentResult.RootCause, agentResult.ChangedSummary, agentResult.ValidationNotes, agentResult.Confidence, agentMeta)
	artifact.MarkAgentNoop()

	diff, err := worktree.CollectDiff(ctx, tree)
	if err != nil {
		artifact.MarkFailedAt(candidate.FailureDiff, err)
		final.Error = sanitize.Secrets(err.Error())
		path, _ := candidate.Write(opts.OutDir, artifact)
		final.ArtifactPath = path
		final.Status = artifact.Status
		return final, nil
	}
	artifact.SetDiff(diff.ChangedFiles, diff.Text)

	validationResult := validation.Run(ctx, validation.Options{
		Command:     opts.TestCommand,
		WorkDir:     tree.Path,
		Timeout:     opts.ValidationTimeout,
		MaxLogBytes: opts.MaxValidationLogBytes,
	})
	artifact.SetValidation(validationResult)
	final.ValidationStatus = validationResult.Status
	final.Status = artifact.Status

	path, writeErr := candidate.Write(opts.OutDir, artifact)
	if writeErr != nil {
		final.Error = sanitize.Secrets(writeErr.Error())
		return final, writeErr
	}
	final.ArtifactPath = path
	final.Rationale = agentResult.Rationale

	if validationResult.Status != candidate.ValidationPassed || artifact.EmptyDiff {
		final.Error = firstNonEmpty(validationResult.Error, "final implementation did not produce a validation-passing non-empty diff")
		return final, nil
	}
	if err := evaluation.WriteFinalPatch(evalResult.FinalPatchPath, artifact.Diff); err != nil {
		final.Error = sanitize.Secrets(err.Error())
		return final, err
	}
	final.UsedFallback = false
	final.FinalPatchPath = evalResult.FinalPatchPath
	events.Emit(Event{Type: eventFinalPatchWritten, LensID: definition.ID, Path: evalResult.FinalPatchPath, Status: "synthesized"})
	return final, nil
}

func findCandidate(results []CandidateResult, id lens.ID) *CandidateResult {
	for idx := range results {
		if results[idx].Artifact.Lens.ID == id {
			return &results[idx]
		}
	}
	return nil
}

func executePreparedCandidates(ctx context.Context, opts Options, runner agent.Runner, prepared []preparedCandidate, results []CandidateResult, events *eventRecorder) {
	type indexedResult struct {
		index  int
		result CandidateResult
	}

	ch := make(chan indexedResult, len(prepared))
	var wg sync.WaitGroup
	agentLimit := make(chan struct{}, agentConcurrency(opts, len(prepared)))
	for _, candidate := range prepared {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- indexedResult{
				index: candidate.Index,
				result: executePreparedCandidate(
					ctx,
					opts,
					runner,
					candidate.Definition,
					candidate.Tree,
					candidate.Artifact,
					agentLimit,
					events,
				),
			}
		}()
	}

	wg.Wait()
	close(ch)
	for item := range ch {
		results[item.index] = item.result
	}
}

func executePreparedCandidate(ctx context.Context, opts Options, runner agent.Runner, definition lens.Definition, tree worktree.Tree, artifact candidate.Artifact, agentLimit chan struct{}, events *eventRecorder) CandidateResult {
	candidateResult := CandidateResult{
		LensID:   definition.ID,
		Artifact: artifact,
	}

	promptPayload, err := prompt.Build(promptContext(opts), definition)
	if err != nil {
		artifact.MarkFailedAt(candidate.FailurePrompt, err)
		return writeCandidate(opts.OutDir, artifact, candidateResult, events)
	}
	promptPath, err := prompt.Write(opts.OutDir, promptPayload)
	if err != nil {
		artifact.MarkFailedAt(candidate.FailurePrompt, err)
		return writeCandidate(opts.OutDir, artifact, candidateResult, events)
	}
	artifact.PromptPath = promptPath
	events.Emit(Event{
		LensID: definition.ID,
		Type:   eventPromptWritten,
		Path:   promptPath,
	})

	agentResult, agentMeta, err := runAgentWithRetry(ctx, opts, runner, definition, tree, promptPayload, promptPath, agentLimit, events)
	artifact.SetAgentResult(agentResult.Rationale, agentResult.RootCause, agentResult.ChangedSummary, agentResult.ValidationNotes, agentResult.Confidence, agentMeta)
	if err != nil {
		artifact.MarkFailedAt(candidate.FailureAgent, err)
		return writeCandidate(opts.OutDir, artifact, candidateResult, events)
	}
	artifact.MarkAgentNoop()

	diff, err := worktree.CollectDiff(ctx, tree)
	if err != nil {
		artifact.MarkFailedAt(candidate.FailureDiff, err)
		return writeCandidate(opts.OutDir, artifact, candidateResult, events)
	}
	artifact.SetDiff(diff.ChangedFiles, diff.Text)
	events.Emit(Event{
		LensID:       definition.ID,
		Type:         eventDiffCollected,
		ChangedFiles: len(diff.ChangedFiles),
	})

	events.Emit(Event{
		LensID: definition.ID,
		Type:   eventValidationStarted,
	})
	validationResult := validation.Run(ctx, validation.Options{
		Command:     opts.TestCommand,
		WorkDir:     tree.Path,
		Timeout:     opts.ValidationTimeout,
		MaxLogBytes: opts.MaxValidationLogBytes,
	})
	artifact.SetValidation(validationResult)
	events.Emit(Event{
		LensID:     definition.ID,
		Type:       eventValidationFinished,
		Status:     string(validationResult.Status),
		DurationMS: validationResult.DurationMS,
		Error:      validationResult.Error,
	})

	return writeCandidate(opts.OutDir, artifact, candidateResult, events)
}

func finalPrompt(opts Options, evalResult evaluation.Result, selected CandidateResult) string {
	var builder strings.Builder
	builder.WriteString("# Varsynth Final Implementation Prompt\n")
	builder.WriteString("You are producing the final patch in an isolated final worktree.\n")
	builder.WriteString("The selected candidate patch has already been applied. Inspect the repository, keep or refine the patch, and do not edit run artifacts.\n")
	builder.WriteString("Run validation when practical and return only the required JSON final response.\n\n")
	builder.WriteString(fmt.Sprintf("- Run ID: %s\n", sanitize.Secrets(opts.RunID)))
	builder.WriteString(fmt.Sprintf("- Selected lens: %s\n", selected.Artifact.Lens.ID))
	builder.WriteString(fmt.Sprintf("- Validation command: %s\n\n", sanitize.Secrets(opts.TestCommand)))
	builder.WriteString("## Selected Candidate\n")
	builder.WriteString(fmt.Sprintf("- Rationale: %s\n", sanitize.Secrets(selected.Artifact.Rationale)))
	builder.WriteString(fmt.Sprintf("- Root cause: %s\n", sanitize.Secrets(selected.Artifact.RootCause)))
	builder.WriteString(fmt.Sprintf("- Changed summary: %s\n", sanitize.Secrets(selected.Artifact.ChangedSummary)))
	builder.WriteString(fmt.Sprintf("- Validation notes: %s\n", sanitize.Secrets(selected.Artifact.ValidationNotes)))
	builder.WriteString("```diff\n")
	builder.WriteString(sanitize.Limit(sanitize.Secrets(selected.Artifact.Diff), 32*1024))
	builder.WriteString("\n```\n\n")
	if evalResult.Critic != nil {
		builder.WriteString("## Critic Guidance\n")
		builder.WriteString(fmt.Sprintf("- Critic selected lens: %s\n", evalResult.Critic.SelectedLensID))
		builder.WriteString(fmt.Sprintf("- Rationale: %s\n", sanitize.Secrets(evalResult.Critic.Rationale)))
		builder.WriteString(fmt.Sprintf("- Disagreement summary: %s\n", sanitize.Secrets(evalResult.Critic.DisagreementSummary)))
		builder.WriteString(fmt.Sprintf("- Risk notes: %s\n", sanitize.Secrets(evalResult.Critic.RiskNotes)))
		builder.WriteString(fmt.Sprintf("- Implementation instructions: %s\n\n", sanitize.Secrets(evalResult.Critic.ImplementationInstructions)))
	}
	builder.WriteString("## Output Expectations\n")
	builder.WriteString("- Leave a focused final diff in this worktree.\n")
	builder.WriteString("- Prefer the selected candidate unless critic guidance identifies a concrete improvement.\n")
	builder.WriteString("- Final response must be a JSON object with `rationale`, `root_cause`, `changed_summary`, `validation_notes`, and `confidence` from 0 to 1 or null.\n")
	return builder.String()
}

func runAgentWithRetry(ctx context.Context, opts Options, runner agent.Runner, definition lens.Definition, tree worktree.Tree, promptPayload prompt.Payload, promptPath string, agentLimit chan struct{}, events *eventRecorder) (agent.Result, candidate.AgentResult, error) {
	maxLogBytes := opts.MaxAgentLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = agent.DefaultMaxLogBytes
	}

	totalAttempts := 1 + opts.AgentRetries
	if totalAttempts < 1 {
		totalAttempts = 1
	}

	meta := candidate.AgentResult{
		LogDir: filepath.Join(opts.OutDir, "agents", string(definition.ID)),
	}
	var lastResult agent.Result
	var lastErr error

	for attempt := 1; attempt <= totalAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		if err := acquire(ctx, agentLimit); err != nil {
			lastErr = err
			break
		}

		events.Emit(Event{
			LensID:  definition.ID,
			Type:    eventAgentStarted,
			Attempt: attempt,
		})
		startedAt := time.Now()
		result, err := runner.Run(ctx, agent.Input{
			RunID:        opts.RunID,
			Lens:         definition,
			WorktreePath: tree.Path,
			TestCommand:  opts.TestCommand,
			Prompt:       promptPayload.Text,
			PromptPath:   promptPath,
		})
		release(agentLimit)

		lastResult = result
		lastErr = err
		attemptMeta := writeAgentArtifacts(meta.LogDir, attempt, result, err, time.Since(startedAt).Milliseconds(), maxLogBytes)
		meta.Attempts = append(meta.Attempts, attemptMeta)
		meta.AttemptCount = len(meta.Attempts)
		meta.Backend = firstNonEmpty(meta.Backend, result.Backend)
		meta.Stdout = result.Stdout
		meta.Stderr = result.Stderr
		meta.FinalResponse = result.FinalResponse
		meta.StdoutPath = filepath.Join(meta.LogDir, "stdout.log")
		meta.StderrPath = filepath.Join(meta.LogDir, "stderr.log")
		meta.FinalResponsePath = filepath.Join(meta.LogDir, "final_response.json")

		if err == nil {
			events.Emit(Event{
				LensID:     definition.ID,
				Type:       eventAgentFinished,
				Attempt:    attempt,
				Status:     "success",
				DurationMS: attemptMeta.DurationMS,
			})
			return result, meta, nil
		}

		events.Emit(Event{
			LensID:     definition.ID,
			Type:       eventAgentFailed,
			Attempt:    attempt,
			Status:     "failed",
			DurationMS: attemptMeta.DurationMS,
			Error:      err.Error(),
		})
		if attempt == totalAttempts {
			break
		}
		if err := sleepBeforeRetry(ctx, opts.AgentRetryDelay, attempt); err != nil {
			lastErr = err
			break
		}
	}

	return lastResult, meta, lastErr
}

func promptContext(opts Options) prompt.Context {
	ctx := opts.PromptContext
	if ctx.RunID == "" {
		ctx.RunID = opts.RunID
	}
	if ctx.RepoRoot == "" {
		ctx.RepoRoot = opts.RepoRoot
	}
	if ctx.BaseCommit == "" {
		ctx.BaseCommit = opts.BaseCommit
	}
	if ctx.TestCommand == "" {
		ctx.TestCommand = opts.TestCommand
	}
	return ctx
}

func collectWriteErrors(results []CandidateResult) []error {
	var writeErrors []error
	for _, result := range results {
		if result.Error != "" && result.ArtifactPath == "" {
			writeErrors = append(writeErrors, fmt.Errorf("%s: %s", result.LensID, result.Error))
		}
	}
	return writeErrors
}

func writeCandidate(outDir string, artifact candidate.Artifact, result CandidateResult, events *eventRecorder) CandidateResult {
	path, err := candidate.Write(outDir, artifact)
	if err != nil {
		result.Error = err.Error()
		result.Artifact = artifact
		return result
	}
	result.ArtifactPath = path
	result.Artifact = artifact
	events.Emit(Event{
		LensID: artifact.Lens.ID,
		Type:   eventCandidateWritten,
		Path:   path,
		Status: string(artifact.Status),
		Error:  artifact.Error,
	})
	return result
}

func buildReport(opts Options, result Result) report.Summary {
	summary := report.Summary{
		RunID:          opts.RunID,
		RepoRoot:       opts.RepoRoot,
		BaseCommit:     opts.BaseCommit,
		TestCommand:    opts.TestCommand,
		OutDir:         opts.OutDir,
		WorktreeRoot:   result.WorktreeRoot,
		RunEventsPath:  result.RunEventsPath,
		EvaluationPath: result.EvaluationPath,
		FinalPatchPath: result.FinalPatchPath,
		CleanupError:   result.CleanupError,
		Candidates:     make([]report.CandidateSummary, 0, len(result.Candidates)),
	}
	if result.Evaluation.ViableCandidateFound {
		summary.SelectedCandidate = &report.SelectedCandidate{
			LensID:       result.Evaluation.SelectedLensID,
			ArtifactPath: result.Evaluation.SelectedArtifactPath,
			Rationale:    result.Evaluation.SelectionRationale,
		}
	}
	if result.Evaluation.Critic != nil {
		summary.Critic = &report.CriticSummary{
			SelectedLensID:             result.Evaluation.Critic.SelectedLensID,
			Rationale:                  result.Evaluation.Critic.Rationale,
			DisagreementSummary:        result.Evaluation.Critic.DisagreementSummary,
			RiskNotes:                  result.Evaluation.Critic.RiskNotes,
			ImplementationInstructions: result.Evaluation.Critic.ImplementationInstructions,
			ArtifactPath:               result.Evaluation.Critic.ArtifactPath,
			PromptPath:                 result.Evaluation.Critic.PromptPath,
			StdoutPath:                 result.Evaluation.Critic.StdoutPath,
			StderrPath:                 result.Evaluation.Critic.StderrPath,
		}
	}
	if result.FinalImplementation != nil {
		summary.FinalImplementation = &report.FinalImplementationSummary{
			Attempted:        result.FinalImplementation.Attempted,
			UsedFallback:     result.FinalImplementation.UsedFallback,
			ArtifactPath:     result.FinalImplementation.ArtifactPath,
			WorktreePath:     result.FinalImplementation.WorktreePath,
			PromptPath:       result.FinalImplementation.PromptPath,
			AgentLogDir:      result.FinalImplementation.AgentLogDir,
			Status:           result.FinalImplementation.Status,
			ValidationStatus: result.FinalImplementation.ValidationStatus,
			Rationale:        result.FinalImplementation.Rationale,
			Error:            result.FinalImplementation.Error,
		}
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

func agentConcurrency(opts Options, prepared int) int {
	if opts.AgentConcurrency > 0 {
		return opts.AgentConcurrency
	}
	if prepared > 0 {
		return prepared
	}
	return 1
}

func acquire(ctx context.Context, limit chan struct{}) error {
	if limit == nil {
		return nil
	}
	select {
	case limit <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(limit chan struct{}) {
	if limit != nil {
		<-limit
	}
}

func sleepBeforeRetry(ctx context.Context, delay time.Duration, attempt int) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay * time.Duration(attempt))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeAgentArtifacts(logDir string, attempt int, result agent.Result, runErr error, durationMS int64, maxLogBytes int) candidate.AgentAttempt {
	status := "success"
	errText := ""
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}

	attemptDir := filepath.Join(logDir, fmt.Sprintf("attempt-%d", attempt))
	stdoutPath := filepath.Join(attemptDir, "stdout.log")
	stderrPath := filepath.Join(attemptDir, "stderr.log")
	finalResponsePath := filepath.Join(attemptDir, "final_response.json")
	latestStdoutPath := filepath.Join(logDir, "stdout.log")
	latestStderrPath := filepath.Join(logDir, "stderr.log")
	latestFinalResponsePath := filepath.Join(logDir, "final_response.json")

	_ = os.MkdirAll(attemptDir, 0o755)
	stdout := []byte(sanitize.Log(result.Stdout, maxLogBytes))
	stderr := []byte(sanitize.Log(result.Stderr, maxLogBytes))
	finalResponse := []byte(sanitize.Log(result.FinalResponse, maxLogBytes))
	_ = os.WriteFile(stdoutPath, stdout, 0o644)
	_ = os.WriteFile(stderrPath, stderr, 0o644)
	_ = os.WriteFile(finalResponsePath, finalResponse, 0o644)
	_ = os.WriteFile(latestStdoutPath, stdout, 0o644)
	_ = os.WriteFile(latestStderrPath, stderr, 0o644)
	_ = os.WriteFile(latestFinalResponsePath, finalResponse, 0o644)

	return candidate.AgentAttempt{
		Attempt:           attempt,
		Status:            status,
		DurationMS:        durationMS,
		Error:             sanitize.Secrets(errText),
		StdoutPath:        stdoutPath,
		StderrPath:        stderrPath,
		FinalResponsePath: finalResponsePath,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
