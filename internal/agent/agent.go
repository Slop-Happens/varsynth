package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Slop-Happens/varsynth/internal/lens"
)

type Input struct {
	RunID        string
	Lens         lens.Definition
	WorktreePath string
}

type Result struct {
	Rationale string `json:"rationale"`
	RootCause string `json:"root_cause"`
}

type Runner interface {
	Run(ctx context.Context, input Input) (Result, error)
}

type Stub struct{}

func (Stub) Run(ctx context.Context, input Input) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if input.Lens.ID == "" {
		return Result{}, fmt.Errorf("agent lens id is required")
	}
	if strings.TrimSpace(input.WorktreePath) == "" {
		return Result{}, fmt.Errorf("agent worktree path is required")
	}

	return Result{
		Rationale: fmt.Sprintf("Stub agent for %s lens did not modify files.", input.Lens.ID),
		RootCause: "Stub agent did not analyze root cause.",
	}, nil
}
