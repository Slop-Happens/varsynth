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

type FinalResponse struct {
	Rationale       string   `json:"rationale"`
	RootCause       string   `json:"root_cause"`
	ChangedSummary  string   `json:"changed_summary"`
	ValidationNotes string   `json:"validation_notes"`
	Confidence      *float64 `json:"confidence,omitempty"`
}

type Input struct {
	RunID        string
	Lens         lens.Definition
	WorktreePath string
	TestCommand  string
	Prompt       string
	PromptPath   string
}

type Result struct {
	Rationale       string   `json:"rationale"`
	RootCause       string   `json:"root_cause"`
	ChangedSummary  string   `json:"changed_summary,omitempty"`
	ValidationNotes string   `json:"validation_notes,omitempty"`
	Confidence      *float64 `json:"confidence,omitempty"`
	Backend         string   `json:"backend,omitempty"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	FinalResponse   string   `json:"final_response,omitempty"`
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
	Rationale       string
	RootCause       string
	ChangedSummary  string
	ValidationNotes string
	Confidence      *float64
	Stdout          string
	Stderr          string
	FinalResponse   string
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
	if response.FinalResponse != "" {
		response.applyFinalResponse(ParseFinalResponse(response.FinalResponse))
	} else if response.Rationale == "" && response.Stdout != "" {
		response.applyFinalResponse(ParseFinalResponse(response.Stdout))
	}

	maxLogBytes := runner.MaxLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = DefaultMaxLogBytes
	}

	result := Result{
		Rationale:       sanitize.Secrets(response.Rationale),
		RootCause:       sanitize.Secrets(response.RootCause),
		ChangedSummary:  sanitize.Secrets(response.ChangedSummary),
		ValidationNotes: sanitize.Secrets(response.ValidationNotes),
		Confidence:      response.Confidence,
		Backend:         runner.Backend.Name(),
		Stdout:          sanitize.Log(response.Stdout, maxLogBytes),
		Stderr:          sanitize.Log(response.Stderr, maxLogBytes),
		FinalResponse:   sanitize.Log(response.FinalResponse, maxLogBytes),
	}
	return result, err
}

type Stub struct{}

func (Stub) Run(ctx context.Context, input Input) (Result, error) {
	if err := validateInput(ctx, input); err != nil {
		return Result{}, err
	}

	return Result{
		Rationale:       fmt.Sprintf("Stub agent for %s lens did not modify files.", input.Lens.ID),
		RootCause:       "Stub agent did not analyze root cause.",
		ChangedSummary:  "No files changed.",
		ValidationNotes: "Stub agent does not run validation.",
		Backend:         "stub",
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

	schemaPath, err := writeOutputSchema()
	if err != nil {
		return Response{}, err
	}
	defer os.Remove(schemaPath)

	args := backend.args(request.WorktreePath, lastMessagePath, schemaPath)

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
		response.FinalResponse = sanitize.Log(string(finalPayload), maxLogBytes)
		response.applyFinalResponse(ParseFinalResponse(response.FinalResponse))
	}
	if response.Rationale == "" {
		response.FinalResponse = sanitize.Log(stdout.String(), maxLogBytes)
		response.applyFinalResponse(ParseFinalResponse(response.FinalResponse))
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

func (backend CodexBackend) args(worktreePath, lastMessagePath, schemaPath string) []string {
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
		"--ephemeral",
		"--output-schema", schemaPath,
		"--output-last-message", lastMessagePath,
	)
	if backend.Model != "" {
		args = append(args, "--model", backend.Model)
	}
	args = append(args, backend.ExtraArgs...)
	return append(args, "-")
}

func ParseFinalResponse(text string) FinalResponse {
	text = strings.TrimSpace(text)
	if text == "" {
		return FinalResponse{}
	}

	var structured FinalResponse
	if err := json.Unmarshal([]byte(text), &structured); err == nil {
		return sanitizeFinalResponse(structured)
	}

	if block := fencedJSON(text); block != "" {
		if err := json.Unmarshal([]byte(block), &structured); err == nil {
			return sanitizeFinalResponse(structured)
		}
	}

	text = sanitize.Secrets(text)
	rationale := section(text, "Rationale")
	rootCause := section(text, "Root Cause")
	if rootCause == "" {
		rootCause = section(text, "RootCause")
	}
	changedSummary := section(text, "Changed Summary")
	if changedSummary == "" {
		changedSummary = section(text, "ChangedSummary")
	}
	validationNotes := section(text, "Validation Notes")
	if validationNotes == "" {
		validationNotes = section(text, "ValidationNotes")
	}
	if rationale != "" || rootCause != "" {
		return sanitizeFinalResponse(FinalResponse{
			Rationale:       rationale,
			RootCause:       rootCause,
			ChangedSummary:  changedSummary,
			ValidationNotes: validationNotes,
		})
	}

	return FinalResponse{Rationale: text}
}

func (response *Response) applyFinalResponse(final FinalResponse) {
	response.Rationale = firstNonEmpty(response.Rationale, final.Rationale)
	response.RootCause = firstNonEmpty(response.RootCause, final.RootCause)
	response.ChangedSummary = firstNonEmpty(response.ChangedSummary, final.ChangedSummary)
	response.ValidationNotes = firstNonEmpty(response.ValidationNotes, final.ValidationNotes)
	if response.Confidence == nil {
		response.Confidence = final.Confidence
	}
}

func sanitizeFinalResponse(response FinalResponse) FinalResponse {
	response.Rationale = strings.TrimSpace(sanitize.Secrets(response.Rationale))
	response.RootCause = strings.TrimSpace(sanitize.Secrets(response.RootCause))
	response.ChangedSummary = strings.TrimSpace(sanitize.Secrets(response.ChangedSummary))
	response.ValidationNotes = strings.TrimSpace(sanitize.Secrets(response.ValidationNotes))
	return response
}

func writeOutputSchema() (string, error) {
	file, err := os.CreateTemp("", "varsynth-codex-schema-*.json")
	if err != nil {
		return "", fmt.Errorf("create codex output schema: %w", err)
	}
	path := file.Name()
	_, writeErr := file.WriteString(finalResponseSchema)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write codex output schema: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close codex output schema: %w", closeErr)
	}
	return path, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

const finalResponseSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["rationale", "root_cause", "changed_summary", "validation_notes", "confidence"],
  "properties": {
    "rationale": {
      "type": "string",
      "description": "What changed and why."
    },
    "root_cause": {
      "type": "string",
      "description": "The underlying defect or failure path addressed."
    },
    "changed_summary": {
      "type": "string",
      "description": "Concise summary of files or behavior changed."
    },
    "validation_notes": {
      "type": "string",
      "description": "Validation command results, skipped validation, or known validation limits."
    },
    "confidence": {
      "type": ["number", "null"],
      "minimum": 0,
      "maximum": 1,
      "description": "Confidence from 0 to 1, or null when not estimated."
    }
  }
}
`

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
