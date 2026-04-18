package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
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
	SelectedLensID lens.ID `json:"selected_lens_id"`
	Rationale      string  `json:"rationale"`
}

type Options struct {
	RunID       string
	OutDir      string
	Selector    string
	CriticMode  string
	Critic      Critic
	FinalPatch  string
}

type Result struct {
	RunID                string            `json:"run_id"`
	Selector             string            `json:"selector"`
	CriticMode           string            `json:"critic_mode"`
	ViableCandidateFound bool              `json:"viable_candidate_found"`
	SelectionDisabled    bool              `json:"selection_disabled,omitempty"`
	SelectedLensID       lens.ID           `json:"selected_lens_id,omitempty"`
	SelectedArtifactPath string            `json:"selected_artifact_path,omitempty"`
	SelectionRationale   string            `json:"selection_rationale"`
	FinalPatchPath       string            `json:"final_patch_path,omitempty"`
	Candidates           []RankedCandidate `json:"candidates"`
}

type RankedCandidate struct {
	Rank             int                      `json:"rank"`
	LensID           lens.ID                  `json:"lens_id"`
	LensName         string                   `json:"lens_name"`
	ArtifactPath     string                   `json:"artifact_path,omitempty"`
	Status           candidate.Status         `json:"status"`
	ValidationStatus candidate.ValidationStatus `json:"validation_status"`
	ChangedFiles     []string                 `json:"changed_files"`
	DiffBytes        int                      `json:"diff_bytes"`
	EmptyDiff        bool                     `json:"empty_diff"`
	Viable           bool                     `json:"viable"`
	Reasons          []string                 `json:"reasons"`
	Error            string                   `json:"error,omitempty"`
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

	evalPath := Path(opts.OutDir)
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal evaluation: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(evalPath, payload, 0o644); err != nil {
		return Result{}, fmt.Errorf("write evaluation: %w", err)
	}

	if result.FinalPatchPath == "" || patch == "" {
		return result, nil
	}

	if err := os.WriteFile(result.FinalPatchPath, []byte(patch), 0o644); err != nil {
		return Result{}, fmt.Errorf("write final patch: %w", err)
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
