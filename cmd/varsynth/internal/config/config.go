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
	CodexFullAuto     bool
	AgentTimeout      time.Duration
	AgentConcurrency  int
	AgentRetries      int
	AgentRetryDelay   time.Duration
	SelectCandidate   string
	CriticMode        string
	FinalPatch        string
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
	fs.StringVar(&cfg.SelectCandidate, "select-candidate", "deterministic", "Candidate selection strategy: deterministic or off")
	fs.StringVar(&cfg.CriticMode, "critic", "off", "Critic mode: off, stub, or codex")
	fs.StringVar(&cfg.FinalPatch, "final-patch", "", "Path for the selected final patch artifact (defaults to <out>/final.patch)")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Execute the bootstrap pipeline without downstream actions")
	fs.BoolVar(&cfg.PreserveWorktrees, "preserve-worktrees", false, "Keep candidate worktrees on disk after the run completes")
	fs.StringVar(&cfg.AgentMode, "agent", AgentStub, "Candidate agent backend: stub or codex")
	fs.StringVar(&cfg.CodexCommand, "codex-command", "codex", "Codex CLI command used when --agent codex")
	fs.StringVar(&cfg.CodexModel, "codex-model", "", "Codex model override used when --agent codex")
	fs.BoolVar(&cfg.CodexFullAuto, "codex-full-auto", true, "Run Codex in full-auto sandboxed mode when --agent codex")
	fs.DurationVar(&cfg.AgentTimeout, "agent-timeout", 0, "Optional timeout for each agent run, for example 10m")
	fs.IntVar(&cfg.AgentConcurrency, "agent-concurrency", 0, "Maximum concurrent agent runs; 0 runs all lenses concurrently")
	fs.IntVar(&cfg.AgentRetries, "agent-retries", 0, "Number of retries after an agent/backend failure")
	fs.DurationVar(&cfg.AgentRetryDelay, "agent-retry-delay", 0, "Base delay between agent retries, for example 2s")

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
	cfg.SelectCandidate = strings.ToLower(strings.TrimSpace(cfg.SelectCandidate))
	cfg.CriticMode = strings.ToLower(strings.TrimSpace(cfg.CriticMode))

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
	if c.AgentConcurrency < 0 {
		errs = append(errs, errors.New("--agent-concurrency must be greater than or equal to 0"))
	}
	if c.AgentRetries < 0 {
		errs = append(errs, errors.New("--agent-retries must be greater than or equal to 0"))
	}
	if c.AgentRetryDelay < 0 {
		errs = append(errs, errors.New("--agent-retry-delay must be greater than or equal to 0"))
	}
	switch strings.ToLower(strings.TrimSpace(c.SelectCandidate)) {
	case "deterministic", "off":
	default:
		errs = append(errs, fmt.Errorf("--select-candidate must be deterministic or off"))
	}
	switch strings.ToLower(strings.TrimSpace(c.CriticMode)) {
	case "off", "stub", "codex":
	default:
		errs = append(errs, fmt.Errorf("--critic must be off, stub, or codex"))
	}

	return errors.Join(errs...)
}
