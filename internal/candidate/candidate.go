package candidate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

const DefaultMaxLogBytes = 64 * 1024

type Status string

const (
	StatusCreated          Status = "created"
	StatusAgentNoop        Status = "agent_noop"
	StatusValidationPassed Status = "validation_passed"
	StatusValidationFailed Status = "validation_failed"
	StatusFailed           Status = "failed"
)

type ValidationStatus string

const (
	ValidationNotRun   ValidationStatus = "not_run"
	ValidationPassed   ValidationStatus = "passed"
	ValidationFailed   ValidationStatus = "failed"
	ValidationTimedOut ValidationStatus = "timed_out"
)

type FailureStage string

const (
	FailureSetup      FailureStage = "setup"
	FailurePrompt     FailureStage = "prompt"
	FailureAgent      FailureStage = "agent"
	FailureDiff       FailureStage = "diff"
	FailureValidation FailureStage = "validation"
)

// Artifact is the persisted output for one lens candidate.
type Artifact struct {
	RunID           string           `json:"run_id"`
	Lens            lens.Definition  `json:"lens"`
	Status          Status           `json:"status"`
	FailureStage    FailureStage     `json:"failure_stage,omitempty"`
	WorktreePath    string           `json:"worktree_path"`
	PromptPath      string           `json:"prompt_path,omitempty"`
	ChangedFiles    []string         `json:"changed_files"`
	Diff            string           `json:"diff"`
	EmptyDiff       bool             `json:"empty_diff"`
	Rationale       string           `json:"rationale"`
	RootCause       string           `json:"root_cause"`
	ChangedSummary  string           `json:"changed_summary,omitempty"`
	ValidationNotes string           `json:"validation_notes,omitempty"`
	Confidence      *float64         `json:"confidence,omitempty"`
	Agent           AgentResult      `json:"agent,omitempty"`
	Validation      ValidationResult `json:"validation"`
	Error           string           `json:"error,omitempty"`
}

type AgentResult struct {
	Backend           string         `json:"backend,omitempty"`
	AttemptCount      int            `json:"attempt_count,omitempty"`
	LogDir            string         `json:"log_dir,omitempty"`
	StdoutPath        string         `json:"stdout_path,omitempty"`
	StderrPath        string         `json:"stderr_path,omitempty"`
	FinalResponsePath string         `json:"final_response_path,omitempty"`
	Stdout            string         `json:"stdout,omitempty"`
	Stderr            string         `json:"stderr,omitempty"`
	FinalResponse     string         `json:"final_response,omitempty"`
	Attempts          []AgentAttempt `json:"attempts,omitempty"`
}

type AgentAttempt struct {
	Attempt           int    `json:"attempt"`
	Status            string `json:"status"`
	DurationMS        int64  `json:"duration_ms"`
	Error             string `json:"error,omitempty"`
	StdoutPath        string `json:"stdout_path,omitempty"`
	StderrPath        string `json:"stderr_path,omitempty"`
	FinalResponsePath string `json:"final_response_path,omitempty"`
}

// ValidationResult records command execution for a candidate worktree.
type ValidationResult struct {
	Command    string           `json:"command,omitempty"`
	Status     ValidationStatus `json:"status"`
	ExitCode   *int             `json:"exit_code,omitempty"`
	DurationMS int64            `json:"duration_ms"`
	TimedOut   bool             `json:"timed_out"`
	Stdout     string           `json:"stdout,omitempty"`
	Stderr     string           `json:"stderr,omitempty"`
	Error      string           `json:"error,omitempty"`
}

func New(runID string, definition lens.Definition, worktreePath string) Artifact {
	return Artifact{
		RunID:        runID,
		Lens:         definition,
		Status:       StatusCreated,
		WorktreePath: worktreePath,
		ChangedFiles: []string{},
		EmptyDiff:    true,
		Rationale:    "stub agent has not produced a rationale yet",
		RootCause:    "stub agent has not produced a root-cause claim yet",
		Validation: ValidationResult{
			Status: ValidationNotRun,
		},
	}
}

func (artifact *Artifact) MarkAgentNoop() {
	artifact.Status = StatusAgentNoop
}

func (artifact *Artifact) MarkFailed(err error) {
	artifact.MarkFailedAt("", err)
}

func (artifact *Artifact) MarkFailedAt(stage FailureStage, err error) {
	artifact.Status = StatusFailed
	artifact.FailureStage = stage
	if err != nil {
		artifact.Error = sanitize.Secrets(err.Error())
	}
}

func (artifact *Artifact) SetDiff(changedFiles []string, diff string) {
	artifact.ChangedFiles = append([]string(nil), changedFiles...)
	artifact.Diff = sanitize.Secrets(diff)
	artifact.EmptyDiff = diff == ""
}

func (artifact *Artifact) SetAgentResult(rationale, rootCause, changedSummary, validationNotes string, confidence *float64, result AgentResult) {
	artifact.Rationale = sanitize.Secrets(rationale)
	artifact.RootCause = sanitize.Secrets(rootCause)
	artifact.ChangedSummary = sanitize.Secrets(changedSummary)
	artifact.ValidationNotes = sanitize.Secrets(validationNotes)
	artifact.Confidence = confidence
	artifact.Agent = AgentResult{
		Backend:           sanitize.Secrets(result.Backend),
		AttemptCount:      result.AttemptCount,
		LogDir:            sanitize.Secrets(result.LogDir),
		StdoutPath:        sanitize.Secrets(result.StdoutPath),
		StderrPath:        sanitize.Secrets(result.StderrPath),
		FinalResponsePath: sanitize.Secrets(result.FinalResponsePath),
		Stdout:            sanitize.Log(result.Stdout, DefaultMaxLogBytes),
		Stderr:            sanitize.Log(result.Stderr, DefaultMaxLogBytes),
		FinalResponse:     sanitize.Log(result.FinalResponse, DefaultMaxLogBytes),
		Attempts:          sanitizeAttempts(result.Attempts),
	}
}

func (artifact *Artifact) SetValidation(result ValidationResult) {
	result.Command = sanitize.Secrets(result.Command)
	result.Stdout = sanitize.Secrets(result.Stdout)
	result.Stderr = sanitize.Secrets(result.Stderr)
	result.Error = sanitize.Secrets(result.Error)
	artifact.Validation = result

	switch result.Status {
	case ValidationPassed:
		artifact.Status = StatusValidationPassed
	case ValidationFailed, ValidationTimedOut:
		artifact.Status = StatusValidationFailed
		artifact.FailureStage = FailureValidation
	}
}

func Dir(outDir string) string {
	return filepath.Join(outDir, "candidates")
}

func Path(outDir string, id lens.ID) string {
	return filepath.Join(Dir(outDir), string(id)+".json")
}

func Write(outDir string, artifact Artifact) (string, error) {
	if artifact.Lens.ID == "" {
		return "", fmt.Errorf("candidate lens id is required")
	}
	artifact = sanitizedArtifact(artifact)

	candidateDir := Dir(outDir)
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		return "", fmt.Errorf("create candidate directory: %w", err)
	}

	path := Path(outDir, artifact.Lens.ID)
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal candidate artifact: %w", err)
	}
	payload = append(payload, '\n')

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return "", fmt.Errorf("write candidate artifact: %w", err)
	}
	return path, nil
}

func sanitizedArtifact(artifact Artifact) Artifact {
	artifact.Diff = sanitize.Secrets(artifact.Diff)
	artifact.Rationale = sanitize.Secrets(artifact.Rationale)
	artifact.RootCause = sanitize.Secrets(artifact.RootCause)
	artifact.ChangedSummary = sanitize.Secrets(artifact.ChangedSummary)
	artifact.ValidationNotes = sanitize.Secrets(artifact.ValidationNotes)
	artifact.Agent.LogDir = sanitize.Secrets(artifact.Agent.LogDir)
	artifact.Agent.StdoutPath = sanitize.Secrets(artifact.Agent.StdoutPath)
	artifact.Agent.StderrPath = sanitize.Secrets(artifact.Agent.StderrPath)
	artifact.Agent.FinalResponsePath = sanitize.Secrets(artifact.Agent.FinalResponsePath)
	artifact.Agent.Stdout = sanitize.Log(artifact.Agent.Stdout, DefaultMaxLogBytes)
	artifact.Agent.Stderr = sanitize.Log(artifact.Agent.Stderr, DefaultMaxLogBytes)
	artifact.Agent.FinalResponse = sanitize.Log(artifact.Agent.FinalResponse, DefaultMaxLogBytes)
	artifact.Agent.Attempts = sanitizeAttempts(artifact.Agent.Attempts)
	artifact.Validation.Command = sanitize.Secrets(artifact.Validation.Command)
	artifact.Validation.Stdout = sanitize.Secrets(artifact.Validation.Stdout)
	artifact.Validation.Stderr = sanitize.Secrets(artifact.Validation.Stderr)
	artifact.Validation.Error = sanitize.Secrets(artifact.Validation.Error)
	artifact.Error = sanitize.Secrets(artifact.Error)
	return artifact
}

func sanitizeAttempts(attempts []AgentAttempt) []AgentAttempt {
	if len(attempts) == 0 {
		return nil
	}
	out := make([]AgentAttempt, len(attempts))
	for idx, attempt := range attempts {
		out[idx] = AgentAttempt{
			Attempt:           attempt.Attempt,
			Status:            sanitize.Secrets(attempt.Status),
			DurationMS:        attempt.DurationMS,
			Error:             sanitize.Secrets(attempt.Error),
			StdoutPath:        sanitize.Secrets(attempt.StdoutPath),
			StderrPath:        sanitize.Secrets(attempt.StderrPath),
			FinalResponsePath: sanitize.Secrets(attempt.FinalResponsePath),
		}
	}
	return out
}
