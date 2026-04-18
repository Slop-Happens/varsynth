package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

type Options struct {
	RepoRoot   string
	BaseCommit string
	RootDir    string
	Preserve   bool
}

type Tree struct {
	LensID     lens.ID `json:"lens_id"`
	Path       string  `json:"path"`
	BaseCommit string  `json:"base_commit"`
}

type Manager struct {
	repoRoot   string
	baseCommit string
	rootDir    string
	ownsRoot   bool
	preserve   bool
	created    []Tree
}

func NewManager(opts Options) (*Manager, error) {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return nil, fmt.Errorf("repo root is required")
	}
	if strings.TrimSpace(opts.BaseCommit) == "" {
		return nil, fmt.Errorf("base commit is required")
	}

	repoRoot, err := filepath.Abs(opts.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repo root: %w", err)
	}

	rootDir := opts.RootDir
	ownsRoot := false
	if strings.TrimSpace(rootDir) == "" {
		rootDir, err = os.MkdirTemp("", "varsynth-worktrees-*")
		if err != nil {
			return nil, fmt.Errorf("create worktree root: %w", err)
		}
		ownsRoot = true
	} else {
		rootDir, err = filepath.Abs(rootDir)
		if err != nil {
			return nil, fmt.Errorf("resolve worktree root: %w", err)
		}
		if err := os.MkdirAll(rootDir, 0o755); err != nil {
			return nil, fmt.Errorf("create worktree root: %w", err)
		}
	}

	return &Manager{
		repoRoot:   repoRoot,
		baseCommit: opts.BaseCommit,
		rootDir:    rootDir,
		ownsRoot:   ownsRoot,
		preserve:   opts.Preserve,
	}, nil
}

func (manager *Manager) RepoRoot() string {
	return manager.repoRoot
}

func (manager *Manager) BaseCommit() string {
	return manager.baseCommit
}

func (manager *Manager) RootDir() string {
	return manager.rootDir
}

func (manager *Manager) Preserve() bool {
	return manager.preserve
}

func (manager *Manager) Created() []Tree {
	trees := make([]Tree, len(manager.created))
	copy(trees, manager.created)
	return trees
}

func (manager *Manager) Create(ctx context.Context, definition lens.Definition) (Tree, error) {
	if definition.ID == "" {
		return Tree{}, fmt.Errorf("lens id is required")
	}

	path := filepath.Join(manager.rootDir, string(definition.ID))
	if err := manager.git(ctx, "worktree", "add", "--detach", path, manager.baseCommit); err != nil {
		return Tree{}, fmt.Errorf("create %s worktree: %w", definition.ID, err)
	}

	tree := Tree{
		LensID:     definition.ID,
		Path:       path,
		BaseCommit: manager.baseCommit,
	}
	manager.created = append(manager.created, tree)
	return tree, nil
}

func (manager *Manager) Cleanup(ctx context.Context) error {
	if manager.preserve {
		return nil
	}

	var errs []error
	for i := len(manager.created) - 1; i >= 0; i-- {
		tree := manager.created[i]
		if err := manager.git(ctx, "worktree", "remove", "--force", tree.Path); err != nil {
			errs = append(errs, fmt.Errorf("remove %s worktree: %w", tree.LensID, err))
		}
	}
	manager.created = nil

	if manager.ownsRoot {
		if err := os.RemoveAll(manager.rootDir); err != nil {
			errs = append(errs, fmt.Errorf("remove worktree root: %w", err))
		}
	}

	return joinErrors(errs)
}

func (manager *Manager) git(ctx context.Context, args ...string) error {
	_, err := git(ctx, manager.repoRoot, args...)
	return err
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, commandError(args, output, err)
	}
	return output, nil
}

func commandError(args []string, output []byte, err error) error {
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, string(output))
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	var builder strings.Builder
	for i, err := range errs {
		if i > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString(err.Error())
	}
	return errors.New(builder.String())
}
