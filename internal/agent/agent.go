package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

const DefaultMaxLogBytes = 64 * 1024

type Input struct {
	RunID        string
	Lens         lens.Definition
	WorktreePath string
	TestCommand  string
	Prompt       string
	PromptPath   string
}

type Result struct {
	Rationale string `json:"rationale"`
	RootCause string `json:"root_cause"`
	Backend   string `json:"backend,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
}

type Runner interface {
	Run(ctx context.Context, input Input) (Result, error)
}

type Request struct {
	RunID        string
	Lens         lens.Definition
	WorktreePath string
	TestCommand  string
	Prompt       string
	PromptPath   string
}

type Response struct {
	Rationale string
	RootCause string
	Stdout    string
	Stderr    string
}

type Backend interface {
	Name() string
	Run(ctx context.Context, request Request) (Response, error)
}

type BackendRunner struct {
	Backend     Backend
	MaxLogBytes int
}

func (runner BackendRunner) Run(ctx context.Context, input Input) (Result, error) {
	if err := validateInput(ctx, input); err != nil {
		return Result{}, err
	}
	if runner.Backend == nil {
		return Result{}, fmt.Errorf("agent backend is required")
	}

	response, err := runner.Backend.Run(ctx, Request{
		RunID:        input.RunID,
		Lens:         input.Lens,
		WorktreePath: input.WorktreePath,
		TestCommand:  input.TestCommand,
		Prompt:       input.Prompt,
		PromptPath:   input.PromptPath,
	})

	maxLogBytes := runner.MaxLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = DefaultMaxLogBytes
	}

	result := Result{
		Rationale: sanitize.Secrets(response.Rationale),
		RootCause: sanitize.Secrets(response.RootCause),
		Backend:   runner.Backend.Name(),
		Stdout:    sanitize.Log(response.Stdout, maxLogBytes),
		Stderr:    sanitize.Log(response.Stderr, maxLogBytes),
	}
	return result, err
}

type Stub struct{}

func (Stub) Run(ctx context.Context, input Input) (Result, error) {
	if err := validateInput(ctx, input); err != nil {
		return Result{}, err
	}

	return Result{
		Rationale: fmt.Sprintf("Stub agent for %s lens did not modify files.", input.Lens.ID),
		RootCause: "Stub agent did not analyze root cause.",
		Backend:   "stub",
	}, nil
}

type CodexBackend struct {
	Command     string
	Model       string
	FullAuto    bool
	Timeout     time.Duration
	ExtraArgs   []string
	MaxLogBytes int
}

func (backend CodexBackend) Name() string {
	command := strings.TrimSpace(backend.Command)
	if command == "" {
		command = "codex"
	}
	return command
}

func (backend CodexBackend) Run(ctx context.Context, request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}

	runCtx := ctx
	cancel := func() {}
	if backend.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, backend.Timeout)
	}
	defer cancel()

	lastMessage, err := os.CreateTemp("", "varsynth-codex-last-*.md")
	if err != nil {
		return Response{}, fmt.Errorf("create codex output file: %w", err)
	}
	lastMessagePath := lastMessage.Name()
	if err := lastMessage.Close(); err != nil {
		return Response{}, fmt.Errorf("close codex output file: %w", err)
	}
	defer os.Remove(lastMessagePath)

	args := backend.args(request.WorktreePath, lastMessagePath)

	command := strings.TrimSpace(backend.Command)
	if command == "" {
		command = "codex"
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = request.WorktreePath
	cmd.Stdin = strings.NewReader(request.Prompt)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	maxLogBytes := backend.MaxLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = DefaultMaxLogBytes
	}
	response := Response{
		Stdout: sanitize.Log(stdout.String(), maxLogBytes),
		Stderr: sanitize.Log(stderr.String(), maxLogBytes),
	}

	finalPayload, readErr := os.ReadFile(lastMessagePath)
	if readErr == nil {
		response.Rationale, response.RootCause = ParseFinalResponse(string(finalPayload))
	}
	if response.Rationale == "" {
		response.Rationale, response.RootCause = ParseFinalResponse(stdout.String())
	}

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return response, runCtx.Err()
	}
	if err != nil {
		return response, fmt.Errorf("codex exec failed: %w", err)
	}
	if readErr != nil && response.Rationale == "" {
		return response, fmt.Errorf("read codex final response: %w", readErr)
	}
	return response, nil
}

func (backend CodexBackend) args(worktreePath, lastMessagePath string) []string {
	args := []string{
		"exec",
		"--cd", worktreePath,
	}
	if backend.FullAuto {
		args = append(args, "--full-auto")
	} else {
		args = append(args, "--sandbox", "workspace-write")
	}
	args = append(args,
		"--skip-git-repo-check",
		"--output-last-message", lastMessagePath,
	)
	if backend.Model != "" {
		args = append(args, "--model", backend.Model)
	}
	args = append(args, backend.ExtraArgs...)
	return append(args, "-")
}

func ParseFinalResponse(text string) (string, string) {
	text = strings.TrimSpace(sanitize.Secrets(text))
	if text == "" {
		return "", ""
	}

	var structured struct {
		Rationale string `json:"rationale"`
		RootCause string `json:"root_cause"`
	}
	if err := json.Unmarshal([]byte(text), &structured); err == nil {
		return strings.TrimSpace(structured.Rationale), strings.TrimSpace(structured.RootCause)
	}

	if block := fencedJSON(text); block != "" {
		if err := json.Unmarshal([]byte(block), &structured); err == nil {
			return strings.TrimSpace(structured.Rationale), strings.TrimSpace(structured.RootCause)
		}
	}

	rationale := section(text, "Rationale")
	rootCause := section(text, "Root Cause")
	if rootCause == "" {
		rootCause = section(text, "RootCause")
	}
	if rationale != "" || rootCause != "" {
		return rationale, rootCause
	}

	return text, ""
}

func validateInput(ctx context.Context, input Input) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Lens.ID == "" {
		return fmt.Errorf("agent lens id is required")
	}
	if strings.TrimSpace(input.WorktreePath) == "" {
		return fmt.Errorf("agent worktree path is required")
	}
	return nil
}

func validateRequest(request Request) error {
	if request.Lens.ID == "" {
		return fmt.Errorf("agent lens id is required")
	}
	if strings.TrimSpace(request.WorktreePath) == "" {
		return fmt.Errorf("agent worktree path is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return fmt.Errorf("agent prompt is required")
	}
	return nil
}

func fencedJSON(text string) string {
	re := regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func section(text, name string) string {
	lines := strings.Split(text, "\n")
	var captured []string
	capturing := false
	headerRE := regexp.MustCompile(`^[A-Za-z][A-Za-z ]{0,40}:`)
	prefix := strings.ToLower(name) + ":"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, prefix) {
			capturing = true
			continue
		}
		if capturing && headerRE.MatchString(trimmed) {
			break
		}
		if capturing {
			captured = append(captured, line)
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:len(prefix)]))
		}
	}
	return strings.TrimSpace(strings.Join(captured, "\n"))
}
