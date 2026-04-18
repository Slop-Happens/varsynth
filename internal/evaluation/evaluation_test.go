package evaluation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestEvaluateSelectsFocusedViableCandidate(t *testing.T) {
	outDir := t.TempDir()

	defensive, _ := lens.Lookup(lens.Defensive)
	performance, _ := lens.Lookup(lens.Performance)

	defensiveArtifact := candidate.New("run-1", defensive, "")
	defensiveArtifact.SetDiff([]string{"a.go", "b.go"}, "diff --git a/a.go b/a.go\n+change\n")
	defensiveArtifact.SetValidation(candidate.ValidationResult{Status: candidate.ValidationPassed})

	performanceArtifact := candidate.New("run-1", performance, "")
	performanceArtifact.SetDiff([]string{"a.go"}, "diff --git a/a.go b/a.go\n+change\n")
	performanceArtifact.SetValidation(candidate.ValidationResult{Status: candidate.ValidationPassed})

	result, err := Evaluate(context.Background(), Options{
		RunID:    "run-1",
		OutDir:   outDir,
		Selector: SelectorDeterministic,
	}, []Input{
		{ArtifactPath: "/tmp/defensive.json", Artifact: defensiveArtifact},
		{ArtifactPath: "/tmp/performance.json", Artifact: performanceArtifact},
	})
	if err != nil {
		t.Fatalf("Evaluate() returned error: %v", err)
	}

	if !result.ViableCandidateFound {
		t.Fatal("ViableCandidateFound = false, want true")
	}
	if result.SelectedLensID != lens.Performance {
		t.Fatalf("SelectedLensID = %q, want %q", result.SelectedLensID, lens.Performance)
	}
	if result.FinalPatchPath != filepath.Join(outDir, "final.patch") {
		t.Fatalf("FinalPatchPath = %q", result.FinalPatchPath)
	}
	patch, err := os.ReadFile(result.FinalPatchPath)
	if err != nil {
		t.Fatalf("ReadFile(final.patch): %v", err)
	}
	if string(patch) != performanceArtifact.Diff {
		t.Fatalf("final.patch contents mismatch:\n%s", string(patch))
	}
}

func TestEvaluateSkipsFailedAndEmptyCandidates(t *testing.T) {
	outDir := t.TempDir()

	minimalist, _ := lens.Lookup(lens.Minimalist)
	architect, _ := lens.Lookup(lens.Architect)

	failedArtifact := candidate.New("run-2", minimalist, "")
	failedArtifact.MarkFailed(os.ErrInvalid)

	emptyArtifact := candidate.New("run-2", architect, "")
	emptyArtifact.SetValidation(candidate.ValidationResult{Status: candidate.ValidationPassed})

	result, err := Evaluate(context.Background(), Options{
		RunID:    "run-2",
		OutDir:   outDir,
		Selector: SelectorDeterministic,
	}, []Input{
		{ArtifactPath: "/tmp/minimalist.json", Artifact: failedArtifact},
		{ArtifactPath: "/tmp/architect.json", Artifact: emptyArtifact},
	})
	if err != nil {
		t.Fatalf("Evaluate() returned error: %v", err)
	}

	if result.ViableCandidateFound {
		t.Fatal("ViableCandidateFound = true, want false")
	}
	if result.SelectedLensID != "" {
		t.Fatalf("SelectedLensID = %q, want empty", result.SelectedLensID)
	}
	if result.FinalPatchPath != "" {
		t.Fatalf("FinalPatchPath = %q, want empty", result.FinalPatchPath)
	}
	if _, err := os.Stat(filepath.Join(outDir, "final.patch")); !os.IsNotExist(err) {
		t.Fatalf("final.patch should not exist: %v", err)
	}

	payload, err := os.ReadFile(Path(outDir))
	if err != nil {
		t.Fatalf("ReadFile(evaluation.json): %v", err)
	}
	var decoded Result
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal(evaluation.json): %v", err)
	}
	if decoded.SelectionRationale == "" {
		t.Fatal("SelectionRationale is empty")
	}
}
