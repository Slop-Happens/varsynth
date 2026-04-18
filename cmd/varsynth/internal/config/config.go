package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const (
	AgentStub  = "stub"
	AgentCodex = "codex"
)

type Config struct {
	RepoPath          string
	IssueFile         string
	TestCommand       string
	OutDir            string
	DryRun            bool
	PreserveWorktrees bool
	AgentMode         string
	CodexCommand      string
	CodexModel        string
	AgentTimeout      time.Duration
}

// Parse converts CLI arguments into a validated config and normalizes path-like fields.
func Parse(args []string, stderr io.Writer) (Config, error) {
	fs := flag.NewFlagSet("varsynth", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg Config
	fs.StringVar(&cfg.RepoPath, "repo", "", "Path to the git repository")
	fs.StringVar(&cfg.IssueFile, "issue-file", "", "Path to the normalized issue JSON file")
	fs.StringVar(&cfg.TestCommand, "test-command", "", "Command used to validate candidate runs")
	fs.StringVar(&cfg.OutDir, "out", "", "Directory for generated artifacts")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Execute the bootstrap pipeline without downstream actions")
	fs.BoolVar(&cfg.PreserveWorktrees, "preserve-worktrees", false, "Keep candidate worktrees on disk after the run completes")
	fs.StringVar(&cfg.AgentMode, "agent", AgentStub, "Candidate agent backend: stub or codex")
	fs.StringVar(&cfg.CodexCommand, "codex-command", "codex", "Codex CLI command used when --agent codex")
	fs.StringVar(&cfg.CodexModel, "codex-model", "", "Codex model override used when --agent codex")
	fs.DurationVar(&cfg.AgentTimeout, "agent-timeout", 0, "Optional timeout for each agent run, for example 10m")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), ", "))
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	cfg.RepoPath = filepath.Clean(cfg.RepoPath)
	cfg.IssueFile = filepath.Clean(cfg.IssueFile)
	cfg.OutDir = filepath.Clean(cfg.OutDir)
	cfg.AgentMode = strings.ToLower(strings.TrimSpace(cfg.AgentMode))
	if cfg.AgentMode == "" {
		cfg.AgentMode = AgentStub
	}
	cfg.CodexCommand = strings.TrimSpace(cfg.CodexCommand)
	cfg.CodexModel = strings.TrimSpace(cfg.CodexModel)

	return cfg, nil
}

// Validate checks that the required CLI flags were provided.
func (c Config) Validate() error {
	var errs []error

	if c.RepoPath == "" {
		errs = append(errs, errors.New("--repo is required"))
	}
	if c.IssueFile == "" {
		errs = append(errs, errors.New("--issue-file is required"))
	}
	if c.OutDir == "" {
		errs = append(errs, errors.New("--out is required"))
	}
	if c.TestCommand == "" {
		errs = append(errs, errors.New("--test-command is required"))
	}
	switch strings.ToLower(strings.TrimSpace(c.AgentMode)) {
	case "", AgentStub, AgentCodex:
	default:
		errs = append(errs, fmt.Errorf("--agent must be %q or %q", AgentStub, AgentCodex))
	}

	return errors.Join(errs...)
}
