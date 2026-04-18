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
	RunID               string                      `json:"run_id"`
	RepoRoot            string                      `json:"repo_root"`
	BaseCommit          string                      `json:"base_commit"`
	TestCommand         string                      `json:"test_command"`
	OutDir              string                      `json:"out_dir"`
	WorktreeRoot        string                      `json:"worktree_root"`
	RunEventsPath       string                      `json:"run_events_path,omitempty"`
	Candidates          []CandidateSummary          `json:"candidates"`
	EvaluationPath      string                      `json:"evaluation_path,omitempty"`
	FinalPatchPath      string                      `json:"final_patch_path,omitempty"`
	SelectedCandidate   *SelectedCandidate          `json:"selected_candidate,omitempty"`
	Critic              *CriticSummary              `json:"critic,omitempty"`
	FinalImplementation *FinalImplementationSummary `json:"final_implementation,omitempty"`
	CleanupError        string                      `json:"cleanup_error,omitempty"`
}

type SelectedCandidate struct {
	LensID       lens.ID `json:"lens_id"`
	ArtifactPath string  `json:"artifact_path,omitempty"`
	Rationale    string  `json:"rationale"`
}

type CriticSummary struct {
	SelectedLensID             lens.ID `json:"selected_lens_id,omitempty"`
	Rationale                  string  `json:"rationale,omitempty"`
	DisagreementSummary        string  `json:"disagreement_summary,omitempty"`
	RiskNotes                  string  `json:"risk_notes,omitempty"`
	ImplementationInstructions string  `json:"implementation_instructions,omitempty"`
	ArtifactPath               string  `json:"artifact_path,omitempty"`
	PromptPath                 string  `json:"prompt_path,omitempty"`
	StdoutPath                 string  `json:"stdout_path,omitempty"`
	StderrPath                 string  `json:"stderr_path,omitempty"`
}

type FinalImplementationSummary struct {
	Attempted        bool                       `json:"attempted"`
	UsedFallback     bool                       `json:"used_fallback"`
	ArtifactPath     string                     `json:"artifact_path,omitempty"`
	WorktreePath     string                     `json:"worktree_path,omitempty"`
	PromptPath       string                     `json:"prompt_path,omitempty"`
	AgentLogDir      string                     `json:"agent_log_dir,omitempty"`
	Status           candidate.Status           `json:"status,omitempty"`
	ValidationStatus candidate.ValidationStatus `json:"validation_status,omitempty"`
	Rationale        string                     `json:"rationale,omitempty"`
	Error            string                     `json:"error,omitempty"`
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
	summary.EvaluationPath = sanitize.Secrets(summary.EvaluationPath)
	summary.FinalPatchPath = sanitize.Secrets(summary.FinalPatchPath)
	summary.CleanupError = sanitize.Secrets(summary.CleanupError)
	if summary.SelectedCandidate != nil {
		summary.SelectedCandidate.ArtifactPath = sanitize.Secrets(summary.SelectedCandidate.ArtifactPath)
		summary.SelectedCandidate.Rationale = sanitize.Secrets(summary.SelectedCandidate.Rationale)
	}
	if summary.Critic != nil {
		summary.Critic.Rationale = sanitize.Secrets(summary.Critic.Rationale)
		summary.Critic.DisagreementSummary = sanitize.Secrets(summary.Critic.DisagreementSummary)
		summary.Critic.RiskNotes = sanitize.Secrets(summary.Critic.RiskNotes)
		summary.Critic.ImplementationInstructions = sanitize.Secrets(summary.Critic.ImplementationInstructions)
		summary.Critic.ArtifactPath = sanitize.Secrets(summary.Critic.ArtifactPath)
		summary.Critic.PromptPath = sanitize.Secrets(summary.Critic.PromptPath)
		summary.Critic.StdoutPath = sanitize.Secrets(summary.Critic.StdoutPath)
		summary.Critic.StderrPath = sanitize.Secrets(summary.Critic.StderrPath)
	}
	if summary.FinalImplementation != nil {
		summary.FinalImplementation.ArtifactPath = sanitize.Secrets(summary.FinalImplementation.ArtifactPath)
		summary.FinalImplementation.WorktreePath = sanitize.Secrets(summary.FinalImplementation.WorktreePath)
		summary.FinalImplementation.PromptPath = sanitize.Secrets(summary.FinalImplementation.PromptPath)
		summary.FinalImplementation.AgentLogDir = sanitize.Secrets(summary.FinalImplementation.AgentLogDir)
		summary.FinalImplementation.Rationale = sanitize.Secrets(summary.FinalImplementation.Rationale)
		summary.FinalImplementation.Error = sanitize.Secrets(summary.FinalImplementation.Error)
	}
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
