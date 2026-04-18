package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type Config struct {
	RepoPath          string
	IssueFile         string
	TestCommand       string
	OutDir            string
	DryRun            bool
	PreserveWorktrees bool
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

	return errors.Join(errs...)
}
