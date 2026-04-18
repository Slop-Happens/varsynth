package agent

import (
	"context"
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
