package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ctxbundle "github.com/Slop-Happens/varsynth/cmd/varsynth/internal/context"
	"github.com/Slop-Happens/varsynth/internal/candidate"
	"github.com/Slop-Happens/varsynth/internal/lens"
	reportpkg "github.com/Slop-Happens/varsynth/internal/report"
)

// TestRunCreatesContextBundle verifies the CLI pipeline emits a context artifact with mapped and unmapped frames.
func TestRunCreatesContextBundle(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, "pkg", "buggy.go"), strings.Join([]string{
		"package pkg",
		"",
		"func buggy() string {",
		`	value := "broken"`,
		"	return value",
		"}",
		"",
	}, "\n"))

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.name", "Varsynth Test")
	runGit(t, repoDir, "config", "user.email", "varsynth@example.com")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")

	issuePath := filepath.Join(t.TempDir(), "issue.json")
	writeFile(t, issuePath, `{
  "id": "ISSUE-42",
  "title": "Example panic",
  "message": "panic: something bad happened",
  "service": "varsynth",
  "environment": "test",
  "stack_frames": [
    {
      "file": "pkg/buggy.go",
      "line": 4,
      "function": "buggy"
    },
    {
      "file": "pkg/missing.go",
      "line": 10,
      "function": "missing"
    }
  ]
}`)

	outDir := filepath.Join(t.TempDir(), "artifacts")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{
		"--repo", repoDir,
		"--issue-file", issuePath,
		"--test-command", "go test ./...",
		"--out", outDir,
		"--dry-run",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v, stderr = %s", err, stderr.String())
	}

	data, err := os.ReadFile(filepath.Join(outDir, "context.json"))
	if err != nil {
		t.Fatalf("read context.json: %v", err)
	}

	var bundle ctxbundle.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal context.json: %v", err)
	}

	if bundle.Issue.ID != "ISSUE-42" {
		t.Fatalf("bundle.Issue.ID = %q", bundle.Issue.ID)
	}
	if bundle.BaseCommit == "" {
		t.Fatal("bundle.BaseCommit is empty")
	}
	if len(bundle.StackFrames) != 2 {
		t.Fatalf("len(bundle.StackFrames) = %d", len(bundle.StackFrames))
	}
	if bundle.StackFrames[0].Status != "mapped" {
		t.Fatalf("first frame status = %q", bundle.StackFrames[0].Status)
	}
	if bundle.StackFrames[1].Status != "unmapped" {
		t.Fatalf("second frame status = %q", bundle.StackFrames[1].Status)
	}
	if len(bundle.Snippets) != 1 {
		t.Fatalf("len(bundle.Snippets) = %d", len(bundle.Snippets))
	}
	if !strings.Contains(stdout.String(), "Dry run: downstream execution skipped") {
		t.Fatalf("stdout missing dry-run message: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "report.json")); !os.IsNotExist(err) {
		t.Fatalf("report.json should not exist in dry-run mode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "candidates")); !os.IsNotExist(err) {
		t.Fatalf("candidates dir should not exist in dry-run mode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "prompts")); !os.IsNotExist(err) {
		t.Fatalf("prompts dir should not exist in dry-run mode: %v", err)
	}
}

func TestRunExecutesCandidatePipeline(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, "go.mod"), "module example.com/demo\n\ngo 1.25.8\n")
	writeFile(t, filepath.Join(repoDir, "pkg", "buggy.go"), strings.Join([]string{
		"package pkg",
		"",
		"func buggy() string {",
		`	value := "broken"`,
		"	return value",
		"}",
		"",
	}, "\n"))

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.name", "Varsynth Test")
	runGit(t, repoDir, "config", "user.email", "varsynth@example.com")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")

	issuePath := filepath.Join(t.TempDir(), "issue.json")
	writeFile(t, issuePath, `{
  "id": "ISSUE-99",
  "title": "Example panic",
  "message": "panic: something bad happened",
  "service": "varsynth",
  "environment": "test",
  "stack_frames": [
    {
      "file": "pkg/buggy.go",
      "line": 4,
      "function": "buggy"
    }
  ]
}`)

	outDir := filepath.Join(t.TempDir(), "artifacts")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{
		"--repo", repoDir,
		"--issue-file", issuePath,
		"--test-command", "test -f go.mod",
		"--out", outDir,
		"--preserve-worktrees",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v, stderr = %s", err, stderr.String())
	}

	contextPath := filepath.Join(outDir, "context.json")
	contextPayload, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read context.json: %v", err)
	}

	var bundle ctxbundle.Bundle
	if err := json.Unmarshal(contextPayload, &bundle); err != nil {
		t.Fatalf("unmarshal context.json: %v", err)
	}

	t.Cleanup(func() {
		worktreeRoot := filepath.Join(outDir, "worktrees")
		for _, definition := range lens.All() {
			path := filepath.Join(worktreeRoot, string(definition.ID))
			if _, err := os.Stat(path); err == nil {
				runGit(t, repoDir, "worktree", "remove", "--force", path)
			}
		}
	})

	for _, definition := range lens.All() {
		path := candidate.Path(outDir, definition.ID)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read candidate artifact %s: %v", definition.ID, err)
		}

		var artifact candidate.Artifact
		if err := json.Unmarshal(payload, &artifact); err != nil {
			t.Fatalf("unmarshal candidate artifact %s: %v", definition.ID, err)
		}
		if artifact.Validation.Status != candidate.ValidationPassed {
			t.Fatalf("%s validation status = %q, want %q", definition.ID, artifact.Validation.Status, candidate.ValidationPassed)
		}
		if artifact.PromptPath != filepath.Join(outDir, "prompts", string(definition.ID)+".md") {
			t.Fatalf("%s PromptPath = %q", definition.ID, artifact.PromptPath)
		}
		promptPayload, err := os.ReadFile(artifact.PromptPath)
		if err != nil {
			t.Fatalf("read prompt artifact %s: %v", definition.ID, err)
		}
		promptText := string(promptPayload)
		if !strings.Contains(promptText, "Example panic") {
			t.Fatalf("%s prompt missing issue title:\n%s", definition.ID, promptText)
		}
		if !strings.Contains(promptText, "pkg/buggy.go") {
			t.Fatalf("%s prompt missing mapped snippet path:\n%s", definition.ID, promptText)
		}
		if artifact.Agent.Backend != "stub" {
			t.Fatalf("%s agent backend = %q, want stub", definition.ID, artifact.Agent.Backend)
		}
		if !artifact.EmptyDiff {
			t.Fatalf("%s EmptyDiff = false, want true for stub agent", definition.ID)
		}
		if strings.TrimSpace(runGitOutput(t, artifact.WorktreePath, "rev-parse", "HEAD")) != bundle.BaseCommit {
			t.Fatalf("%s worktree HEAD does not match base commit", definition.ID)
		}
	}

	reportPayload, err := os.ReadFile(reportpkg.Path(outDir))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}

	var summary reportpkg.Summary
	if err := json.Unmarshal(reportPayload, &summary); err != nil {
		t.Fatalf("unmarshal report.json: %v", err)
	}
	if len(summary.Candidates) != len(lens.All()) {
		t.Fatalf("report candidate count = %d, want %d", len(summary.Candidates), len(lens.All()))
	}

	if !strings.Contains(stdout.String(), "Candidate artifacts: 4") {
		t.Fatalf("stdout missing candidate artifact count: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Agent: stub") {
		t.Fatalf("stdout missing agent mode: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Prompt artifacts: 4") {
		t.Fatalf("stdout missing prompt artifact count: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Validation: passed=4 failed=0 timed_out=0 not_run=0") {
		t.Fatalf("stdout missing validation summary: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Report: "+reportpkg.Path(outDir)) {
		t.Fatalf("stdout missing report path: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Worktrees: preserved at "+filepath.Join(outDir, "worktrees")) {
		t.Fatalf("stdout missing worktree summary: %s", stdout.String())
	}
}

// writeFile creates parent directories and writes fixture content for the test repo.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runGit executes a git command inside the temporary test repository.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}
