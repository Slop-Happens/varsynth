package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

type Summary struct {
	RunID         string             `json:"run_id"`
	RepoRoot      string             `json:"repo_root"`
	BaseCommit    string             `json:"base_commit"`
	TestCommand   string             `json:"test_command"`
	OutDir        string             `json:"out_dir"`
	WorktreeRoot  string             `json:"worktree_root"`
	RunEventsPath string             `json:"run_events_path,omitempty"`
	Candidates    []CandidateSummary `json:"candidates"`
	CleanupError  string             `json:"cleanup_error,omitempty"`
}

type CandidateSummary struct {
	LensID                 lens.ID                    `json:"lens_id"`
	LensName               string                     `json:"lens_name"`
	ArtifactPath           string                     `json:"artifact_path,omitempty"`
	Status                 candidate.Status           `json:"status"`
	FailureStage           candidate.FailureStage     `json:"failure_stage,omitempty"`
	WorktreePath           string                     `json:"worktree_path,omitempty"`
	PromptPath             string                     `json:"prompt_path,omitempty"`
	AgentBackend           string                     `json:"agent_backend,omitempty"`
	AgentAttemptCount      int                        `json:"agent_attempt_count,omitempty"`
	AgentLogDir            string                     `json:"agent_log_dir,omitempty"`
	AgentStdoutPath        string                     `json:"agent_stdout_path,omitempty"`
	AgentStderrPath        string                     `json:"agent_stderr_path,omitempty"`
	AgentFinalResponsePath string                     `json:"agent_final_response_path,omitempty"`
	ChangedFileCount       int                        `json:"changed_file_count"`
	ChangedFiles           []string                   `json:"changed_files"`
	EmptyDiff              bool                       `json:"empty_diff"`
	DiffBytes              int                        `json:"diff_bytes"`
	RationalePresent       bool                       `json:"rationale_present"`
	RootCausePresent       bool                       `json:"root_cause_present"`
	ChangedSummaryPresent  bool                       `json:"changed_summary_present"`
	ValidationNotesPresent bool                       `json:"validation_notes_present"`
	Confidence             *float64                   `json:"confidence,omitempty"`
	ValidationStatus       candidate.ValidationStatus `json:"validation_status"`
	ValidationExit         *int                       `json:"validation_exit_code,omitempty"`
	ValidationMS           int64                      `json:"validation_duration_ms"`
	Error                  string                     `json:"error,omitempty"`
}

func FromArtifact(artifactPath string, artifact candidate.Artifact, err string) CandidateSummary {
	changedFiles := append([]string(nil), artifact.ChangedFiles...)
	return CandidateSummary{
		LensID:                 artifact.Lens.ID,
		LensName:               artifact.Lens.Name,
		ArtifactPath:           artifactPath,
		Status:                 artifact.Status,
		FailureStage:           artifact.FailureStage,
		WorktreePath:           artifact.WorktreePath,
		PromptPath:             artifact.PromptPath,
		AgentBackend:           artifact.Agent.Backend,
		AgentAttemptCount:      artifact.Agent.AttemptCount,
		AgentLogDir:            artifact.Agent.LogDir,
		AgentStdoutPath:        artifact.Agent.StdoutPath,
		AgentStderrPath:        artifact.Agent.StderrPath,
		AgentFinalResponsePath: artifact.Agent.FinalResponsePath,
		ChangedFileCount:       len(changedFiles),
		ChangedFiles:           changedFiles,
		EmptyDiff:              artifact.EmptyDiff,
		DiffBytes:              len(artifact.Diff),
		RationalePresent:       artifact.Rationale != "",
		RootCausePresent:       artifact.RootCause != "",
		ChangedSummaryPresent:  artifact.ChangedSummary != "",
		ValidationNotesPresent: artifact.ValidationNotes != "",
		Confidence:             artifact.Confidence,
		ValidationStatus:       artifact.Validation.Status,
		ValidationExit:         artifact.Validation.ExitCode,
		ValidationMS:           artifact.Validation.DurationMS,
		Error:                  firstNonEmpty(err, artifact.Error),
	}
}

func Path(outDir string) string {
	return filepath.Join(outDir, "report.json")
}

func Write(outDir string, summary Summary) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create report directory: %w", err)
	}
	summary = sanitizedSummary(summary)

	path := Path(outDir)
	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	payload = append(payload, '\n')

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	return path, nil
}

func sanitizedSummary(summary Summary) Summary {
	summary.TestCommand = sanitize.Secrets(summary.TestCommand)
	summary.RunEventsPath = sanitize.Secrets(summary.RunEventsPath)
	summary.CleanupError = sanitize.Secrets(summary.CleanupError)
	for idx := range summary.Candidates {
		summary.Candidates[idx].Error = sanitize.Secrets(summary.Candidates[idx].Error)
		summary.Candidates[idx].AgentLogDir = sanitize.Secrets(summary.Candidates[idx].AgentLogDir)
		summary.Candidates[idx].AgentStdoutPath = sanitize.Secrets(summary.Candidates[idx].AgentStdoutPath)
		summary.Candidates[idx].AgentStderrPath = sanitize.Secrets(summary.Candidates[idx].AgentStderrPath)
		summary.Candidates[idx].AgentFinalResponsePath = sanitize.Secrets(summary.Candidates[idx].AgentFinalResponsePath)
	}
	return summary
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
