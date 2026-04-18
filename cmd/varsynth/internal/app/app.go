package app

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/config"
	ctxbundle "github.com/Slop-Happens/varsynth/cmd/varsynth/internal/context"
	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/issue"
	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/repo"
)

// Run executes the offline bootstrap pipeline from CLI input to context artifact.
func Run(args []string, stdout, stderr io.Writer) error {
	cfg, err := config.Parse(args, stderr)
	if err != nil {
		return err
	}

	meta, err := repo.Inspect(cfg.RepoPath)
	if err != nil {
		return err
	}

	iss, err := issue.Load(cfg.IssueFile)
	if err != nil {
		return err
	}

	bundle, err := ctxbundle.Build(cfg, meta, iss)
	if err != nil {
		return err
	}

	if err := repo.EnsureDir(cfg.OutDir); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	contextPath := filepath.Join(cfg.OutDir, "context.json")
	if err := ctxbundle.Write(contextPath, bundle); err != nil {
		return err
	}

	mappedCount := len(bundle.Snippets)
	fmt.Fprintf(stdout, "Run ID: %s\n", bundle.RunID)
	fmt.Fprintf(stdout, "Repository: %s\n", bundle.RepoRoot)
	fmt.Fprintf(stdout, "Base: %s @ %s\n", bundle.BaseBranch, bundle.BaseCommit)
	fmt.Fprintf(stdout, "Dirty: %t\n", bundle.DirtyState.Dirty)
	fmt.Fprintf(stdout, "Issue: %s (%s)\n", bundle.Issue.Title, bundle.Issue.ID)
	fmt.Fprintf(stdout, "Frames: %d total, %d mapped, %d unmapped\n", len(bundle.StackFrames), mappedCount, len(bundle.StackFrames)-mappedCount)
	fmt.Fprintf(stdout, "Artifact: %s\n", contextPath)
	if cfg.DryRun {
		fmt.Fprintln(stdout, "Dry run: downstream execution skipped")
	}

	return nil
}
