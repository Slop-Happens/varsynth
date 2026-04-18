package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

func TestBuildRendersDeterministicSharedAndLensContext(t *testing.T) {
	definition, ok := lens.Lookup(lens.Defensive)
	if !ok {
		t.Fatal("lens.Lookup(Defensive) returned false")
	}

	ctx := Context{
		RunID:       "run-1",
		RepoRoot:    "/home/alice/projects/payment-service",
		BaseBranch:  "main",
		BaseCommit:  "abc123",
		TestCommand: "go test ./...",
		Issue: Issue{
			ID:          "ISSUE-1",
			Title:       "panic on missing customer",
			Message:     "panic: missing customer token=super-secret-token",
			Service:     "checkout",
			Environment: "prod",
		},
		StackFrames: []StackFrame{
			{
				Index:     2,
				File:      "pkg/checkout/customer.go",
				Line:      42,
				Function:  "LoadCustomer",
				Status:    "mapped",
				SnippetID: "snippet-2",
			},
			{
				Index:    1,
				File:     "pkg/checkout/handler.go",
				Line:     12,
				Function: "Handle",
				Status:   "mapped",
			},
		},
		Snippets: []Snippet{
			{
				ID:        "snippet-2",
				File:      "pkg/checkout/customer.go",
				StartLine: 40,
				EndLine:   43,
				FocusLine: 42,
				SourceLines: []string{
					"func LoadCustomer(id string) Customer {",
					`  api_key := "sk-secretvalue12345"`,
					"  return customers[id]",
					"}",
				},
			},
		},
	}

	first, err := Build(ctx, definition)
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	second, err := Build(ctx, definition)
	if err != nil {
		t.Fatalf("Build() second call returned error: %v", err)
	}

	if first.Text != second.Text {
		t.Fatal("Build() is not deterministic for identical input")
	}
	for _, want := range []string{
		"# Varsynth Candidate Prompt",
		"- Prompt Version: varsynth-prompt-v1",
		"- Lens: Defensive (defensive)",
		"Prioritize robust handling of malformed inputs",
		"- Repository: payment-service",
		"- Title: panic on missing customer",
		"- #1 pkg/checkout/handler.go:12 Handle status=mapped snippet=none",
		"- #2 pkg/checkout/customer.go:42 LoadCustomer status=mapped snippet=snippet-2",
		"### snippet-2 pkg/checkout/customer.go:40-43 focus=42",
		"[REDACTED]",
	} {
		if !strings.Contains(first.Text, want) {
			t.Fatalf("prompt missing %q:\n%s", want, first.Text)
		}
	}
	if strings.Contains(first.Text, "super-secret-token") || strings.Contains(first.Text, "sk-secretvalue12345") {
		t.Fatalf("prompt leaked secret:\n%s", first.Text)
	}
}

func TestWritePersistsPromptArtifact(t *testing.T) {
	definition, ok := lens.Lookup(lens.Minimalist)
	if !ok {
		t.Fatal("lens.Lookup(Minimalist) returned false")
	}

	payload, err := Build(Context{RunID: "run-1"}, definition)
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	path, err := Write(t.TempDir(), payload)
	if err != nil {
		t.Fatalf("Write() returned error: %v", err)
	}
	if filepath.Base(path) != "minimalist.md" {
		t.Fatalf("prompt path = %q, want minimalist.md", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("prompt artifact does not end with newline")
	}
}
