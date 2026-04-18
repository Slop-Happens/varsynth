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
