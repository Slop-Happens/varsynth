package candidate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Slop-Happens/varsynth/internal/lens"
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

// Artifact is the persisted output for one lens candidate.
type Artifact struct {
	RunID        string           `json:"run_id"`
	Lens         lens.Definition  `json:"lens"`
	Status       Status           `json:"status"`
	WorktreePath string           `json:"worktree_path"`
	ChangedFiles []string         `json:"changed_files"`
	Diff         string           `json:"diff"`
	EmptyDiff    bool             `json:"empty_diff"`
	Rationale    string           `json:"rationale"`
	RootCause    string           `json:"root_cause"`
	Validation   ValidationResult `json:"validation"`
	Error        string           `json:"error,omitempty"`
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
	artifact.Status = StatusFailed
	if err != nil {
		artifact.Error = err.Error()
	}
}

func (artifact *Artifact) SetDiff(changedFiles []string, diff string) {
	artifact.ChangedFiles = append([]string(nil), changedFiles...)
	artifact.Diff = diff
	artifact.EmptyDiff = diff == ""
}

func (artifact *Artifact) SetValidation(result ValidationResult) {
	artifact.Validation = result

	switch result.Status {
	case ValidationPassed:
		artifact.Status = StatusValidationPassed
	case ValidationFailed, ValidationTimedOut:
		artifact.Status = StatusValidationFailed
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
