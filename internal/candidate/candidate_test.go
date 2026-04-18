package candidate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestNewInitializesNoopArtifactShape(t *testing.T) {
	definition, ok := lens.Lookup(lens.Defensive)
	if !ok {
		t.Fatal("lens.Lookup(Defensive) returned false")
	}

	artifact := New("run-1", definition, "/tmp/worktree")

	if artifact.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", artifact.RunID)
	}
	if artifact.Lens.ID != lens.Defensive {
		t.Fatalf("Lens.ID = %q, want %q", artifact.Lens.ID, lens.Defensive)
	}
	if artifact.Status != StatusCreated {
		t.Fatalf("Status = %q, want %q", artifact.Status, StatusCreated)
	}
	if artifact.WorktreePath != "/tmp/worktree" {
		t.Fatalf("WorktreePath = %q, want /tmp/worktree", artifact.WorktreePath)
	}
	if artifact.ChangedFiles == nil {
		t.Fatal("ChangedFiles is nil")
	}
	if !artifact.EmptyDiff {
		t.Fatal("EmptyDiff = false, want true")
	}
	if artifact.Rationale == "" {
		t.Fatal("Rationale is empty")
	}
	if artifact.RootCause == "" {
		t.Fatal("RootCause is empty")
	}
	if artifact.Validation.Status != ValidationNotRun {
		t.Fatalf("Validation.Status = %q, want %q", artifact.Validation.Status, ValidationNotRun)
	}
}

func TestSetDiffCopiesChangedFiles(t *testing.T) {
	artifact := Artifact{}
	changedFiles := []string{"main.go"}

	artifact.SetDiff(changedFiles, "diff --git a/main.go b/main.go\n")
	changedFiles[0] = "mutated.go"

	if artifact.ChangedFiles[0] != "main.go" {
		t.Fatalf("ChangedFiles[0] = %q, want main.go", artifact.ChangedFiles[0])
	}
	if artifact.EmptyDiff {
		t.Fatal("EmptyDiff = true, want false")
	}

	artifact.SetDiff(nil, "")
	if artifact.ChangedFiles != nil {
		t.Fatalf("ChangedFiles = %#v, want nil", artifact.ChangedFiles)
	}
	if !artifact.EmptyDiff {
		t.Fatal("EmptyDiff = false, want true")
	}
}

func TestSetValidationUpdatesStatus(t *testing.T) {
	artifact := Artifact{}

	artifact.SetValidation(ValidationResult{Status: ValidationPassed})
	if artifact.Status != StatusValidationPassed {
		t.Fatalf("Status = %q, want %q", artifact.Status, StatusValidationPassed)
	}

	artifact.SetValidation(ValidationResult{Status: ValidationFailed})
	if artifact.Status != StatusValidationFailed {
		t.Fatalf("Status = %q, want %q", artifact.Status, StatusValidationFailed)
	}

	artifact.Status = StatusFailed
	artifact.SetValidation(ValidationResult{Status: ValidationNotRun})
	if artifact.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", artifact.Status, StatusFailed)
	}
}

func TestWritePersistsCandidateJSON(t *testing.T) {
	definition, ok := lens.Lookup(lens.Performance)
	if !ok {
		t.Fatal("lens.Lookup(Performance) returned false")
	}
	artifact := New("run-2", definition, "/tmp/worktree-performance")
	artifact.MarkAgentNoop()
	artifact.SetDiff([]string{"internal/foo.go"}, "diff --git a/internal/foo.go b/internal/foo.go\n")

	outDir := t.TempDir()
	path, err := Write(outDir, artifact)
	if err != nil {
		t.Fatalf("Write() returned error: %v", err)
	}

	wantPath := filepath.Join(outDir, "candidates", "performance.json")
	if path != wantPath {
		t.Fatalf("Write() path = %q, want %q", path, wantPath)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	if payload[len(payload)-1] != '\n' {
		t.Fatal("candidate JSON does not end with newline")
	}

	var got Artifact
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() returned error: %v", err)
	}
	if got.Lens.ID != lens.Performance {
		t.Fatalf("Lens.ID = %q, want %q", got.Lens.ID, lens.Performance)
	}
	if got.Status != StatusAgentNoop {
		t.Fatalf("Status = %q, want %q", got.Status, StatusAgentNoop)
	}
	if len(got.ChangedFiles) != 1 || got.ChangedFiles[0] != "internal/foo.go" {
		t.Fatalf("ChangedFiles = %#v, want internal/foo.go", got.ChangedFiles)
	}
	if got.EmptyDiff {
		t.Fatal("EmptyDiff = true, want false")
	}
}

func TestWriteRejectsMissingLensID(t *testing.T) {
	_, err := Write(t.TempDir(), Artifact{})
	if err == nil {
		t.Fatal("Write() returned nil error")
	}
}

func TestWriteRedactsSecrets(t *testing.T) {
	definition, ok := lens.Lookup(lens.Defensive)
	if !ok {
		t.Fatal("lens.Lookup(Defensive) returned false")
	}
	artifact := New("run-secret", definition, "/tmp/worktree")
	artifact.Rationale = "used token=secret-token-value"
	artifact.RootCause = "api_key: sk-secretvalue12345"
	artifact.Diff = "+ password = hunter2\n"
	artifact.Agent.Stdout = "Authorization: Bearer secretbearer"
	artifact.Validation.Command = "echo token=command-secret"
	artifact.Validation.Stderr = "SECRET=super-secret"

	path, err := Write(t.TempDir(), artifact)
	if err != nil {
		t.Fatalf("Write() returned error: %v", err)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	text := string(payload)
	for _, leaked := range []string{"secret-token-value", "sk-secretvalue12345", "hunter2", "secretbearer", "command-secret", "super-secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("candidate artifact leaked %q:\n%s", leaked, text)
		}
	}
}
