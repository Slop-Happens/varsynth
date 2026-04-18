package candidate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

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
	RunID        string           `json:"run_id"`
	Lens         lens.Definition  `json:"lens"`
	Status       Status           `json:"status"`
	FailureStage FailureStage     `json:"failure_stage,omitempty"`
	WorktreePath string           `json:"worktree_path"`
	PromptPath   string           `json:"prompt_path,omitempty"`
	ChangedFiles []string         `json:"changed_files"`
	Diff         string           `json:"diff"`
	EmptyDiff    bool             `json:"empty_diff"`
	Rationale    string           `json:"rationale"`
	RootCause    string           `json:"root_cause"`
	Agent        AgentResult      `json:"agent,omitempty"`
	Validation   ValidationResult `json:"validation"`
	Error        string           `json:"error,omitempty"`
}

type AgentResult struct {
	Backend string `json:"backend,omitempty"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
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

func (artifact *Artifact) SetAgentResult(rationale, rootCause string, result AgentResult) {
	artifact.Rationale = sanitize.Secrets(rationale)
	artifact.RootCause = sanitize.Secrets(rootCause)
	artifact.Agent = AgentResult{
		Backend: sanitize.Secrets(result.Backend),
		Stdout:  sanitize.Log(result.Stdout, 0),
		Stderr:  sanitize.Log(result.Stderr, 0),
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
	artifact.Agent.Stdout = sanitize.Secrets(artifact.Agent.Stdout)
	artifact.Agent.Stderr = sanitize.Secrets(artifact.Agent.Stderr)
	artifact.Validation.Command = sanitize.Secrets(artifact.Validation.Command)
	artifact.Validation.Stdout = sanitize.Secrets(artifact.Validation.Stdout)
	artifact.Validation.Stderr = sanitize.Secrets(artifact.Validation.Stderr)
	artifact.Validation.Error = sanitize.Secrets(artifact.Validation.Error)
	artifact.Error = sanitize.Secrets(artifact.Error)
	return artifact
}
