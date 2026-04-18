package agent

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestStubRunReturnsPlaceholders(t *testing.T) {
	definition, ok := lens.Lookup(lens.Architect)
	if !ok {
		t.Fatal("lens.Lookup(Architect) returned false")
	}

	result, err := Stub{}.Run(context.Background(), Input{
		RunID:        "run-1",
		Lens:         definition,
		WorktreePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if !strings.Contains(result.Rationale, string(lens.Architect)) {
		t.Fatalf("Rationale = %q, want lens id", result.Rationale)
	}
	if result.RootCause == "" {
		t.Fatal("RootCause is empty")
	}
}

func TestStubRunValidatesInput(t *testing.T) {
	definition, ok := lens.Lookup(lens.Defensive)
	if !ok {
		t.Fatal("lens.Lookup(Defensive) returned false")
	}

	tests := []struct {
		name  string
		input Input
	}{
		{
			name: "missing lens",
			input: Input{
				WorktreePath: t.TempDir(),
			},
		},
		{
			name: "missing worktree path",
			input: Input{
				Lens: definition,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Stub{}.Run(context.Background(), tt.input)
			if err == nil {
				t.Fatal("Run() returned nil error")
			}
		})
	}
}

func TestStubRunHonorsCanceledContext(t *testing.T) {
	definition, ok := lens.Lookup(lens.Minimalist)
	if !ok {
		t.Fatal("lens.Lookup(Minimalist) returned false")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Stub{}.Run(ctx, Input{
		Lens:         definition,
		WorktreePath: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
}

func TestStubImplementsRunner(t *testing.T) {
	var _ Runner = Stub{}
}

func TestBackendRunnerPassesPromptAndCapturesOutput(t *testing.T) {
	definition, ok := lens.Lookup(lens.Performance)
	if !ok {
		t.Fatal("lens.Lookup(Performance) returned false")
	}
	backend := &recordingBackend{
		response: Response{
			FinalResponse: `{"rationale":"changed cache handling","root_cause":"stale cache entry","changed_summary":"updated cache code","validation_notes":"not run","confidence":0.75}`,
			Stdout:        "token=secret-token-value",
			Stderr:        "warning",
		},
	}

	result, err := BackendRunner{Backend: backend}.Run(context.Background(), Input{
		RunID:        "run-1",
		Lens:         definition,
		WorktreePath: t.TempDir(),
		TestCommand:  "go test ./...",
		Prompt:       "repair prompt",
		PromptPath:   "/tmp/prompts/performance.md",
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if backend.request.Prompt != "repair prompt" {
		t.Fatalf("backend prompt = %q, want repair prompt", backend.request.Prompt)
	}
	if result.Backend != "recording" {
		t.Fatalf("Backend = %q, want recording", result.Backend)
	}
	if result.Rationale != "changed cache handling" {
		t.Fatalf("Rationale = %q", result.Rationale)
	}
	if result.RootCause != "stale cache entry" {
		t.Fatalf("RootCause = %q", result.RootCause)
	}
	if result.ChangedSummary != "updated cache code" {
		t.Fatalf("ChangedSummary = %q", result.ChangedSummary)
	}
	if result.ValidationNotes != "not run" {
		t.Fatalf("ValidationNotes = %q", result.ValidationNotes)
	}
	if result.Confidence == nil || *result.Confidence != 0.75 {
		t.Fatalf("Confidence = %v, want 0.75", result.Confidence)
	}
	if strings.Contains(result.Stdout, "secret-token-value") {
		t.Fatalf("Stdout leaked secret: %q", result.Stdout)
	}
}

func TestBackendRunnerReturnsPartialResultOnFailure(t *testing.T) {
	definition, ok := lens.Lookup(lens.Defensive)
	if !ok {
		t.Fatal("lens.Lookup(Defensive) returned false")
	}
	backend := &recordingBackend{
		response: Response{
			Rationale: "partial rationale",
			RootCause: "partial root cause",
			Stderr:    "backend failed",
		},
		err: fmt.Errorf("backend boom"),
	}

	result, err := BackendRunner{Backend: backend}.Run(context.Background(), Input{
		RunID:        "run-1",
		Lens:         definition,
		WorktreePath: t.TempDir(),
		Prompt:       "repair prompt",
	})
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if result.Rationale != "partial rationale" {
		t.Fatalf("partial Rationale = %q", result.Rationale)
	}
	if result.Stderr != "backend failed" {
		t.Fatalf("partial Stderr = %q", result.Stderr)
	}
}

func TestParseFinalResponse(t *testing.T) {
	final := ParseFinalResponse(strings.Join([]string{
		"Rationale:",
		"Updated nil handling.",
		"Root Cause:",
		"Missing guard before dereference.",
		"Changed Summary:",
		"Added nil check.",
		"Validation Notes:",
		"go test passed.",
	}, "\n"))
	if final.Rationale != "Updated nil handling." {
		t.Fatalf("rationale = %q", final.Rationale)
	}
	if final.RootCause != "Missing guard before dereference." {
		t.Fatalf("rootCause = %q", final.RootCause)
	}
	if final.ChangedSummary != "Added nil check." {
		t.Fatalf("changedSummary = %q", final.ChangedSummary)
	}
	if final.ValidationNotes != "go test passed." {
		t.Fatalf("validationNotes = %q", final.ValidationNotes)
	}

	final = ParseFinalResponse(`{"rationale":"small patch","root_cause":"bad branch","changed_summary":"one file","validation_notes":"passed","confidence":0.9}`)
	if final.Rationale != "small patch" || final.RootCause != "bad branch" {
		t.Fatalf("json parse = %q / %q", final.Rationale, final.RootCause)
	}
	if final.Confidence == nil || *final.Confidence != 0.9 {
		t.Fatalf("json confidence = %v, want 0.9", final.Confidence)
	}
}

func TestCodexBackendArgsUseFullAutoByDefault(t *testing.T) {
	args := CodexBackend{
		Model:    "gpt-5.4",
		FullAuto: true,
	}.args("/tmp/worktree", "/tmp/last.md", "/tmp/schema.json")

	joined := strings.Join(args, " ")
	for _, want := range []string{"exec", "--cd /tmp/worktree", "--full-auto", "--skip-git-repo-check", "--ephemeral", "--output-schema /tmp/schema.json", "--output-last-message /tmp/last.md", "--model gpt-5.4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "--sandbox workspace-write") {
		t.Fatalf("args %q should not include explicit sandbox when full-auto is enabled", joined)
	}
}

func TestCodexBackendArgsCanDisableFullAuto(t *testing.T) {
	args := CodexBackend{}.args("/tmp/worktree", "/tmp/last.md", "/tmp/schema.json")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--full-auto") {
		t.Fatalf("args %q should not include --full-auto", joined)
	}
	if !strings.Contains(joined, "--sandbox workspace-write") {
		t.Fatalf("args %q missing workspace sandbox", joined)
	}
}

func TestColoredLineWriterPrefixesAndColors(t *testing.T) {
	var output bytes.Buffer
	writer := newColoredLineWriter(&output, lens.Defensive)

	_, err := writer.Write([]byte("first\nsecond"))
	if err != nil {
		t.Fatalf("Write() returned error: %v", err)
	}
	writer.Flush()

	text := output.String()
	if !strings.Contains(text, "\x1b[36m[defensive]\x1b[0m first\n") {
		t.Fatalf("stream output missing colored first line: %q", text)
	}
	if !strings.Contains(text, "\x1b[36m[defensive]\x1b[0m second\n") {
		t.Fatalf("stream output missing flushed second line: %q", text)
	}
}

type recordingBackend struct {
	request  Request
	response Response
	err      error
}

func (backend *recordingBackend) Name() string {
	return "recording"
}

func (backend *recordingBackend) Run(ctx context.Context, request Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	backend.request = request
	return backend.response, backend.err
}
