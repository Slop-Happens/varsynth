package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

const (
	SelectorDeterministic = "deterministic"
	SelectorOff           = "off"

	CriticOff   = "off"
	CriticStub  = "stub"
	CriticCodex = "codex"
)

type Input struct {
	ArtifactPath string
	Artifact     candidate.Artifact
	Error        string
}

type Critic interface {
	Review(ctx context.Context, input CriticInput) (CriticResult, error)
}

type CriticInput struct {
	RunID      string
	Candidates []RankedCandidate
}

type CriticResult struct {
	SelectedLensID             lens.ID `json:"selected_lens_id"`
	Rationale                  string  `json:"rationale"`
	DisagreementSummary        string  `json:"disagreement_summary,omitempty"`
	RiskNotes                  string  `json:"risk_notes,omitempty"`
	ImplementationInstructions string  `json:"implementation_instructions,omitempty"`
	ArtifactPath               string  `json:"artifact_path,omitempty"`
	PromptPath                 string  `json:"prompt_path,omitempty"`
	StdoutPath                 string  `json:"stdout_path,omitempty"`
	StderrPath                 string  `json:"stderr_path,omitempty"`
}

type Options struct {
	RunID      string
	OutDir     string
	Selector   string
	CriticMode string
	Critic     Critic
	FinalPatch string
}

type Result struct {
	RunID                string                     `json:"run_id"`
	Selector             string                     `json:"selector"`
	CriticMode           string                     `json:"critic_mode"`
	ViableCandidateFound bool                       `json:"viable_candidate_found"`
	SelectionDisabled    bool                       `json:"selection_disabled,omitempty"`
	SelectedLensID       lens.ID                    `json:"selected_lens_id,omitempty"`
	SelectedArtifactPath string                     `json:"selected_artifact_path,omitempty"`
	SelectionRationale   string                     `json:"selection_rationale"`
	FinalPatchPath       string                     `json:"final_patch_path,omitempty"`
	Critic               *CriticResult              `json:"critic,omitempty"`
	FinalImplementation  *FinalImplementationResult `json:"final_implementation,omitempty"`
	Candidates           []RankedCandidate          `json:"candidates"`
}

type RankedCandidate struct {
	Rank             int                        `json:"rank"`
	LensID           lens.ID                    `json:"lens_id"`
	LensName         string                     `json:"lens_name"`
	ArtifactPath     string                     `json:"artifact_path,omitempty"`
	Status           candidate.Status           `json:"status"`
	ValidationStatus candidate.ValidationStatus `json:"validation_status"`
	ChangedFiles     []string                   `json:"changed_files"`
	DiffBytes        int                        `json:"diff_bytes"`
	EmptyDiff        bool                       `json:"empty_diff"`
	Viable           bool                       `json:"viable"`
	Reasons          []string                   `json:"reasons"`
	Error            string                     `json:"error,omitempty"`
	Rationale        string                     `json:"-"`
	RootCause        string                     `json:"-"`
	ChangedSummary   string                     `json:"-"`
	ValidationNotes  string                     `json:"-"`
	Diff             string                     `json:"-"`
}

type FinalImplementationResult struct {
	Attempted        bool                       `json:"attempted"`
	UsedFallback     bool                       `json:"used_fallback"`
	ArtifactPath     string                     `json:"artifact_path,omitempty"`
	WorktreePath     string                     `json:"worktree_path,omitempty"`
	PromptPath       string                     `json:"prompt_path,omitempty"`
	AgentLogDir      string                     `json:"agent_log_dir,omitempty"`
	Status           candidate.Status           `json:"status,omitempty"`
	ValidationStatus candidate.ValidationStatus `json:"validation_status,omitempty"`
	FinalPatchPath   string                     `json:"final_patch_path,omitempty"`
	Rationale        string                     `json:"rationale,omitempty"`
	Error            string                     `json:"error,omitempty"`
}

type scoredCandidate struct {
	input Input
	rank  RankedCandidate
}

func Evaluate(ctx context.Context, opts Options, inputs []Input) (Result, error) {
	selector := opts.Selector
	if selector == "" {
		selector = SelectorDeterministic
	}
	criticMode := opts.CriticMode
	if criticMode == "" {
		criticMode = CriticOff
	}

	result := Result{
		RunID:      opts.RunID,
		Selector:   selector,
		CriticMode: criticMode,
		Candidates: rankCandidates(inputs),
	}

	if selector == SelectorOff {
		result.SelectionDisabled = true
		result.SelectionRationale = "candidate selection disabled"
		return writeArtifacts(opts, result, "")
	}

	selected, ok := chooseDeterministic(result.Candidates)
	if !ok {
		result.SelectionRationale = "no viable candidate passed validation with a non-empty diff"
		return writeArtifacts(opts, result, "")
	}

	result.ViableCandidateFound = true
	result.SelectedLensID = selected.LensID
	result.SelectedArtifactPath = selected.ArtifactPath
	result.SelectionRationale = buildRationale(selected)

	if criticMode != CriticOff && opts.Critic != nil {
		criticResult, err := opts.Critic.Review(ctx, CriticInput{
			RunID:      opts.RunID,
			Candidates: result.Candidates,
		})
		if err == nil {
			result.Critic = &criticResult
		}
		if err == nil && criticResult.SelectedLensID != "" {
			for _, candidate := range result.Candidates {
				if candidate.LensID == criticResult.SelectedLensID && candidate.Viable {
					result.SelectedLensID = candidate.LensID
					result.SelectedArtifactPath = candidate.ArtifactPath
					if strings.TrimSpace(criticResult.Rationale) != "" {
						result.SelectionRationale = criticResult.Rationale
					}
					selected = candidate
					break
				}
			}
		}
	}

	patchPath := opts.FinalPatch
	if strings.TrimSpace(patchPath) == "" {
		patchPath = FinalPatchPath(opts.OutDir)
	}

	patch := diffForLens(inputs, result.SelectedLensID)
	if patch != "" {
		result.FinalPatchPath = patchPath
		return writeArtifacts(opts, result, patch)
	}

	result.FinalPatchPath = ""
	return writeArtifacts(opts, result, "")
}

func WriteResult(outDir string, result Result) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create evaluation directory: %w", err)
	}
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal evaluation: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(Path(outDir), payload, 0o644); err != nil {
		return fmt.Errorf("write evaluation: %w", err)
	}
	return nil
}

func WriteFinalPatch(path, patch string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("final patch path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create final patch directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(patch), 0o644); err != nil {
		return fmt.Errorf("write final patch: %w", err)
	}
	return nil
}

func Path(outDir string) string {
	return filepath.Join(outDir, "evaluation.json")
}

func FinalPatchPath(outDir string) string {
	return filepath.Join(outDir, "final.patch")
}

func writeArtifacts(opts Options, result Result, patch string) (Result, error) {
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create evaluation directory: %w", err)
	}

	if err := WriteResult(opts.OutDir, result); err != nil {
		return Result{}, err
	}

	if result.FinalPatchPath == "" || patch == "" {
		return result, nil
	}

	if err := WriteFinalPatch(result.FinalPatchPath, patch); err != nil {
		return Result{}, err
	}
	return result, nil
}

func rankCandidates(inputs []Input) []RankedCandidate {
	scored := make([]scoredCandidate, 0, len(inputs))
	for _, input := range inputs {
		rank := RankedCandidate{
			LensID:           input.Artifact.Lens.ID,
			LensName:         input.Artifact.Lens.Name,
			ArtifactPath:     input.ArtifactPath,
			Status:           input.Artifact.Status,
			ValidationStatus: input.Artifact.Validation.Status,
			ChangedFiles:     append([]string(nil), input.Artifact.ChangedFiles...),
			DiffBytes:        len(input.Artifact.Diff),
			EmptyDiff:        input.Artifact.EmptyDiff,
			Error:            firstNonEmpty(input.Error, input.Artifact.Error),
			Rationale:        input.Artifact.Rationale,
			RootCause:        input.Artifact.RootCause,
			ChangedSummary:   input.Artifact.ChangedSummary,
			ValidationNotes:  input.Artifact.ValidationNotes,
			Diff:             input.Artifact.Diff,
		}
		rank.Reasons = viabilityReasons(input.Artifact, rank.Error)
		rank.Viable = isViable(input.Artifact, rank.Error)
		scored = append(scored, scoredCandidate{
			input: input,
			rank:  rank,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		left := scored[i].rank
		right := scored[j].rank

		if left.Viable != right.Viable {
			return left.Viable
		}
		if left.Viable {
			if len(left.ChangedFiles) != len(right.ChangedFiles) {
				return len(left.ChangedFiles) < len(right.ChangedFiles)
			}
			if left.DiffBytes != right.DiffBytes {
				return left.DiffBytes < right.DiffBytes
			}
		}
		if left.ValidationStatus != right.ValidationStatus {
			return validationOrder(left.ValidationStatus) < validationOrder(right.ValidationStatus)
		}
		if left.EmptyDiff != right.EmptyDiff {
			return !left.EmptyDiff
		}
		return string(left.LensID) < string(right.LensID)
	})

	ranked := make([]RankedCandidate, 0, len(scored))
	for idx, scoredCandidate := range scored {
		scoredCandidate.rank.Rank = idx + 1
		ranked = append(ranked, scoredCandidate.rank)
	}
	return ranked
}

type StubCritic struct{}

func (StubCritic) Review(ctx context.Context, input CriticInput) (CriticResult, error) {
	if err := ctx.Err(); err != nil {
		return CriticResult{}, err
	}
	for _, candidate := range input.Candidates {
		if candidate.Viable {
			return CriticResult{
				SelectedLensID:             candidate.LensID,
				Rationale:                  fmt.Sprintf("stub critic accepted the top viable %s candidate", candidate.LensID),
				DisagreementSummary:        "stub critic did not perform semantic disagreement analysis",
				RiskNotes:                  "stub critic is deterministic and should be replaced with codex for live analysis",
				ImplementationInstructions: "Use the selected candidate as the baseline and keep the final patch focused.",
			}, nil
		}
	}
	return CriticResult{Rationale: "stub critic found no viable candidates"}, nil
}

type CodexCritic struct {
	Command     string
	Model       string
	FullAuto    bool
	Timeout     time.Duration
	OutDir      string
	MaxLogBytes int
}

func (critic CodexCritic) Review(ctx context.Context, input CriticInput) (CriticResult, error) {
	if strings.TrimSpace(critic.OutDir) == "" {
		return CriticResult{}, fmt.Errorf("critic output directory is required")
	}

	criticDir := filepath.Join(critic.OutDir, "critic")
	if err := os.MkdirAll(criticDir, 0o755); err != nil {
		return CriticResult{}, fmt.Errorf("create critic directory: %w", err)
	}

	promptPath := filepath.Join(criticDir, "prompt.md")
	stdoutPath := filepath.Join(criticDir, "stdout.log")
	stderrPath := filepath.Join(criticDir, "stderr.log")
	finalPath := filepath.Join(criticDir, "critic.json")

	prompt := criticPrompt(input)
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return CriticResult{}, fmt.Errorf("write critic prompt: %w", err)
	}

	runCtx := ctx
	cancel := func() {}
	if critic.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, critic.Timeout)
	}
	defer cancel()

	lastMessage, err := os.CreateTemp("", "varsynth-critic-last-*.json")
	if err != nil {
		return CriticResult{}, fmt.Errorf("create critic output file: %w", err)
	}
	lastMessagePath := lastMessage.Name()
	if err := lastMessage.Close(); err != nil {
		return CriticResult{}, fmt.Errorf("close critic output file: %w", err)
	}
	defer os.Remove(lastMessagePath)

	schemaPath, err := writeCriticSchema()
	if err != nil {
		return CriticResult{}, err
	}
	defer os.Remove(schemaPath)

	command := strings.TrimSpace(critic.Command)
	if command == "" {
		command = "codex"
	}
	args := []string{"exec", "--cd", critic.OutDir}
	if critic.FullAuto {
		args = append(args, "--full-auto")
	} else {
		args = append(args, "--sandbox", "workspace-write")
	}
	args = append(args, "--skip-git-repo-check", "--ephemeral", "--output-schema", schemaPath, "--output-last-message", lastMessagePath)
	if critic.Model != "" {
		args = append(args, "--model", critic.Model)
	}
	args = append(args, "-")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = critic.OutDir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	maxLogBytes := critic.MaxLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = 64 * 1024
	}
	_ = os.WriteFile(stdoutPath, []byte(sanitize.Log(stdout.String(), maxLogBytes)), 0o644)
	_ = os.WriteFile(stderrPath, []byte(sanitize.Log(stderr.String(), maxLogBytes)), 0o644)

	finalPayload, readErr := os.ReadFile(lastMessagePath)
	if readErr != nil {
		finalPayload = stdout.Bytes()
	}
	finalText := sanitize.Log(string(finalPayload), maxLogBytes)
	_ = os.WriteFile(finalPath, []byte(finalText), 0o644)

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return CriticResult{}, runCtx.Err()
	}
	if runErr != nil {
		return CriticResult{}, fmt.Errorf("codex critic failed: %w", runErr)
	}

	var result CriticResult
	if err := json.Unmarshal([]byte(finalText), &result); err != nil {
		return CriticResult{}, fmt.Errorf("parse critic response: %w", err)
	}
	result.Rationale = sanitize.Secrets(result.Rationale)
	result.DisagreementSummary = sanitize.Secrets(result.DisagreementSummary)
	result.RiskNotes = sanitize.Secrets(result.RiskNotes)
	result.ImplementationInstructions = sanitize.Secrets(result.ImplementationInstructions)
	result.ArtifactPath = finalPath
	result.PromptPath = promptPath
	result.StdoutPath = stdoutPath
	result.StderrPath = stderrPath
	return result, nil
}

func criticPrompt(input CriticInput) string {
	var builder strings.Builder
	builder.WriteString("# Varsynth Critic Review\n")
	builder.WriteString("Compare the candidate fixes and choose the best baseline for the final implementation agent.\n")
	builder.WriteString("Do not edit files. Return only the requested JSON object.\n\n")
	builder.WriteString(fmt.Sprintf("Run ID: %s\n\n", sanitize.Secrets(input.RunID)))
	for _, candidate := range input.Candidates {
		builder.WriteString(fmt.Sprintf("## Candidate %s rank=%d viable=%t\n", candidate.LensID, candidate.Rank, candidate.Viable))
		builder.WriteString(fmt.Sprintf("- Status: %s validation=%s\n", candidate.Status, candidate.ValidationStatus))
		builder.WriteString(fmt.Sprintf("- Changed files: %s\n", strings.Join(candidate.ChangedFiles, ", ")))
		builder.WriteString(fmt.Sprintf("- Rationale: %s\n", sanitize.Secrets(candidate.Rationale)))
		builder.WriteString(fmt.Sprintf("- Root cause: %s\n", sanitize.Secrets(candidate.RootCause)))
		builder.WriteString(fmt.Sprintf("- Changed summary: %s\n", sanitize.Secrets(candidate.ChangedSummary)))
		builder.WriteString(fmt.Sprintf("- Validation notes: %s\n", sanitize.Secrets(candidate.ValidationNotes)))
		builder.WriteString("Diff:\n```diff\n")
		builder.WriteString(sanitize.Limit(sanitize.Secrets(candidate.Diff), 24*1024))
		builder.WriteString("\n```\n\n")
	}
	builder.WriteString("Return JSON with selected_lens_id, rationale, disagreement_summary, risk_notes, and implementation_instructions.\n")
	return builder.String()
}

func writeCriticSchema() (string, error) {
	file, err := os.CreateTemp("", "varsynth-critic-schema-*.json")
	if err != nil {
		return "", fmt.Errorf("create critic schema: %w", err)
	}
	path := file.Name()
	_, writeErr := file.WriteString(criticSchema)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write critic schema: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close critic schema: %w", closeErr)
	}
	return path, nil
}

const criticSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["selected_lens_id", "rationale", "disagreement_summary", "risk_notes", "implementation_instructions"],
  "properties": {
    "selected_lens_id": {"type": "string"},
    "rationale": {"type": "string"},
    "disagreement_summary": {"type": "string"},
    "risk_notes": {"type": "string"},
    "implementation_instructions": {"type": "string"}
  }
}
`

func chooseDeterministic(candidates []RankedCandidate) (RankedCandidate, bool) {
	if len(candidates) == 0 {
		return RankedCandidate{}, false
	}
	if !candidates[0].Viable {
		return RankedCandidate{}, false
	}
	return candidates[0], true
}

func buildRationale(selected RankedCandidate) string {
	return fmt.Sprintf(
		"selected %s because it passed validation, produced a non-empty diff, and had the most focused viable change set (%d changed files, %d diff bytes)",
		selected.LensID,
		len(selected.ChangedFiles),
		selected.DiffBytes,
	)
}

func diffForLens(inputs []Input, id lens.ID) string {
	for _, input := range inputs {
		if input.Artifact.Lens.ID == id {
			return input.Artifact.Diff
		}
	}
	return ""
}

func isViable(artifact candidate.Artifact, err string) bool {
	if firstNonEmpty(err, artifact.Error) != "" {
		return false
	}
	if artifact.Status == candidate.StatusFailed {
		return false
	}
	if artifact.Validation.Status != candidate.ValidationPassed {
		return false
	}
	if artifact.EmptyDiff || strings.TrimSpace(artifact.Diff) == "" {
		return false
	}
	return true
}

func viabilityReasons(artifact candidate.Artifact, err string) []string {
	reasons := []string{}
	if firstNonEmpty(err, artifact.Error) != "" {
		reasons = append(reasons, "candidate failed")
	}
	if artifact.Validation.Status != candidate.ValidationPassed {
		reasons = append(reasons, fmt.Sprintf("validation status %s", artifact.Validation.Status))
	}
	if artifact.EmptyDiff || strings.TrimSpace(artifact.Diff) == "" {
		reasons = append(reasons, "empty diff")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "viable")
	}
	return reasons
}

func validationOrder(status candidate.ValidationStatus) int {
	switch status {
	case candidate.ValidationPassed:
		return 0
	case candidate.ValidationNotRun:
		return 1
	case candidate.ValidationTimedOut:
		return 2
	case candidate.ValidationFailed:
		return 3
	default:
		return 4
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
