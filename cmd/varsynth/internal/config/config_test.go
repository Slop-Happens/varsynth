package config

import (
	"bytes"
	"testing"
	"time"
)

func TestParseAgentDefaultsToStub(t *testing.T) {
	cfg, err := Parse([]string{
		"--repo", "/repo",
		"--issue-file", "/issue.json",
		"--test-command", "go test ./...",
		"--out", "/out",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	if cfg.AgentMode != AgentStub {
		t.Fatalf("AgentMode = %q, want %q", cfg.AgentMode, AgentStub)
	}
	if cfg.CodexCommand != "codex" {
		t.Fatalf("CodexCommand = %q, want codex", cfg.CodexCommand)
	}
}

func TestParseCodexAgentOptions(t *testing.T) {
	cfg, err := Parse([]string{
		"--repo", "/repo",
		"--issue-file", "/issue.json",
		"--test-command", "go test ./...",
		"--out", "/out",
		"--agent", "codex",
		"--codex-command", "/bin/codex",
		"--codex-model", "gpt-5.4",
		"--agent-timeout", "10m",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	if cfg.AgentMode != AgentCodex {
		t.Fatalf("AgentMode = %q, want %q", cfg.AgentMode, AgentCodex)
	}
	if cfg.CodexCommand != "/bin/codex" {
		t.Fatalf("CodexCommand = %q, want /bin/codex", cfg.CodexCommand)
	}
	if cfg.CodexModel != "gpt-5.4" {
		t.Fatalf("CodexModel = %q, want gpt-5.4", cfg.CodexModel)
	}
	if cfg.AgentTimeout != 10*time.Minute {
		t.Fatalf("AgentTimeout = %s, want 10m", cfg.AgentTimeout)
	}
}

func TestParseRejectsUnknownAgent(t *testing.T) {
	_, err := Parse([]string{
		"--repo", "/repo",
		"--issue-file", "/issue.json",
		"--test-command", "go test ./...",
		"--out", "/out",
		"--agent", "other",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Parse() returned nil error")
	}
}
