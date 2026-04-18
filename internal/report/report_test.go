package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestFromArtifactSummarizesCandidate(t *testing.T) {
	definition, ok := lens.Lookup(lens.Performance)
	if !ok {
		t.Fatal("lens.Lookup(Performance) returned false")
	}
	artifact := candidate.New("run-1", definition, "/tmp/worktree")
	artifact.Status = candidate.StatusValidationPassed
	artifact.SetDiff([]string{"a.go", "b.go"}, "diff")
	artifact.Validation.Status = candidate.ValidationPassed
	artifact.Validation.DurationMS = 123
	exitCode := 0
	artifact.Validation.ExitCode = &exitCode

	changedFiles := artifact.ChangedFiles
	summary := FromArtifact("/tmp/performance.json", artifact, "")
	changedFiles[0] = "mutated.go"

	if summary.LensID != lens.Performance {
		t.Fatalf("LensID = %q, want %q", summary.LensID, lens.Performance)
	}
	if summary.LensName != "Performance" {
		t.Fatalf("LensName = %q, want Performance", summary.LensName)
	}
	if summary.ArtifactPath != "/tmp/performance.json" {
		t.Fatalf("ArtifactPath = %q, want /tmp/performance.json", summary.ArtifactPath)
	}
	if summary.ChangedFileCount != 2 {
		t.Fatalf("ChangedFileCount = %d, want 2", summary.ChangedFileCount)
	}
	if summary.ChangedFiles[0] != "a.go" {
		t.Fatalf("ChangedFiles[0] = %q, want a.go", summary.ChangedFiles[0])
	}
	if summary.ValidationStatus != candidate.ValidationPassed {
		t.Fatalf("ValidationStatus = %q, want %q", summary.ValidationStatus, candidate.ValidationPassed)
	}
	if summary.DiffBytes != 4 {
		t.Fatalf("DiffBytes = %d, want 4", summary.DiffBytes)
	}
	if !summary.RationalePresent {
		t.Fatal("RationalePresent = false, want true")
	}
	if !summary.RootCausePresent {
		t.Fatal("RootCausePresent = false, want true")
	}
	if summary.ValidationMS != 123 {
		t.Fatalf("ValidationMS = %d, want 123", summary.ValidationMS)
	}
	if summary.ValidationExit == nil || *summary.ValidationExit != 0 {
		t.Fatalf("ValidationExit = %v, want 0", summary.ValidationExit)
	}
}

func TestFromArtifactPrefersWriteError(t *testing.T) {
	definition, ok := lens.Lookup(lens.Minimalist)
	if !ok {
		t.Fatal("lens.Lookup(Minimalist) returned false")
	}
	artifact := candidate.New("run-1", definition, "/tmp/worktree")
	artifact.MarkFailed(os.ErrInvalid)

	summary := FromArtifact("", artifact, "write failed")
	if summary.Error != "write failed" {
		t.Fatalf("Error = %q, want write failed", summary.Error)
	}
}

func TestWritePersistsReportJSON(t *testing.T) {
	outDir := t.TempDir()
	summary := Summary{
		RunID:        "run-1",
		RepoRoot:     "/repo",
		BaseCommit:   "abc123",
		TestCommand:  "make test",
		OutDir:       outDir,
		WorktreeRoot: "/worktrees",
		Candidates: []CandidateSummary{
			{
				LensID:           lens.Defensive,
				LensName:         "Defensive",
				ArtifactPath:     filepath.Join(outDir, "candidates", "defensive.json"),
				Status:           candidate.StatusValidationPassed,
				ChangedFileCount: 0,
				ChangedFiles:     []string{},
				EmptyDiff:        true,
				DiffBytes:        0,
				RationalePresent: true,
				RootCausePresent: true,
				ValidationStatus: candidate.ValidationPassed,
				ValidationMS:     12,
			},
		},
	}

	path, err := Write(outDir, summary)
	if err != nil {
		t.Fatalf("Write() returned error: %v", err)
	}
	if path != filepath.Join(outDir, "report.json") {
		t.Fatalf("path = %q, want report.json under outDir", path)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	if payload[len(payload)-1] != '\n' {
		t.Fatal("report JSON does not end with newline")
	}

	var got Summary
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() returned error: %v", err)
	}
	if got.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", got.RunID)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(got.Candidates))
	}
}
