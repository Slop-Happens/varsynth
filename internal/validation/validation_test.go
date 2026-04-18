package validation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Slop-Happens/varsynth/internal/candidate"
)

func TestRunPassesCommand(t *testing.T) {
	result := Run(context.Background(), Options{
		Command: "printf hello",
		WorkDir: t.TempDir(),
	})

	if result.Status != candidate.ValidationPassed {
		t.Fatalf("Status = %q, want %q; error=%q", result.Status, candidate.ValidationPassed, result.Error)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("ExitCode = %v, want 0", result.ExitCode)
	}
	if result.Stdout != "hello" {
		t.Fatalf("Stdout = %q, want hello", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
	if result.Command != "printf hello" {
		t.Fatalf("Command = %q, want printf hello", result.Command)
	}
}

func TestRunFailsCommand(t *testing.T) {
	result := Run(context.Background(), Options{
		Command: "echo boom >&2; exit 7",
		WorkDir: t.TempDir(),
	})

	if result.Status != candidate.ValidationFailed {
		t.Fatalf("Status = %q, want %q", result.Status, candidate.ValidationFailed)
	}
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("ExitCode = %v, want 7", result.ExitCode)
	}
	if strings.TrimSpace(result.Stderr) != "boom" {
		t.Fatalf("Stderr = %q, want boom", result.Stderr)
	}
	if result.Error == "" {
		t.Fatal("Error is empty")
	}
}

func TestRunTimesOut(t *testing.T) {
	result := Run(context.Background(), Options{
		Command: "sleep 1",
		WorkDir: t.TempDir(),
		Timeout: 20 * time.Millisecond,
	})

	if result.Status != candidate.ValidationTimedOut {
		t.Fatalf("Status = %q, want %q", result.Status, candidate.ValidationTimedOut)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}
	if result.Error == "" {
		t.Fatal("Error is empty")
	}
}

func TestRunUsesWorkDir(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "marker.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	result := Run(context.Background(), Options{
		Command: "test -f marker.txt",
		WorkDir: workDir,
	})

	if result.Status != candidate.ValidationPassed {
		t.Fatalf("Status = %q, want %q; error=%q", result.Status, candidate.ValidationPassed, result.Error)
	}
}

func TestRunBoundsLogs(t *testing.T) {
	result := Run(context.Background(), Options{
		Command:     "printf 1234567890; printf abcdefghij >&2",
		WorkDir:     t.TempDir(),
		MaxLogBytes: 8,
	})

	if result.Status != candidate.ValidationPassed {
		t.Fatalf("Status = %q, want %q", result.Status, candidate.ValidationPassed)
	}
	if len(result.Stdout) > 8 {
		t.Fatalf("Stdout length = %d, want <= 8", len(result.Stdout))
	}
	if len(result.Stderr) > 8 {
		t.Fatalf("Stderr length = %d, want <= 8", len(result.Stderr))
	}
	if result.Stdout == "1234567890" {
		t.Fatal("Stdout was not truncated")
	}
	if result.Stderr == "abcdefghij" {
		t.Fatal("Stderr was not truncated")
	}
}

func TestRunRejectsMissingCommand(t *testing.T) {
	result := Run(context.Background(), Options{WorkDir: t.TempDir()})

	if result.Status != candidate.ValidationFailed {
		t.Fatalf("Status = %q, want %q", result.Status, candidate.ValidationFailed)
	}
	if result.Error == "" {
		t.Fatal("Error is empty")
	}
}

func TestRunRejectsMissingWorkDir(t *testing.T) {
	result := Run(context.Background(), Options{Command: "true"})

	if result.Status != candidate.ValidationFailed {
		t.Fatalf("Status = %q, want %q", result.Status, candidate.ValidationFailed)
	}
	if result.Error == "" {
		t.Fatal("Error is empty")
	}
}
