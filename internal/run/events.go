package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

const (
	eventPromptWritten      = "prompt_written"
	eventAgentStarted       = "agent_started"
	eventAgentFailed        = "agent_failed"
	eventAgentFinished      = "agent_finished"
	eventDiffCollected      = "diff_collected"
	eventValidationStarted  = "validation_started"
	eventValidationFinished = "validation_finished"
	eventCandidateWritten   = "candidate_written"
	eventCriticStarted      = "critic_started"
	eventCriticFinished     = "critic_finished"
	eventFinalAgentStarted  = "final_agent_started"
	eventFinalAgentFinished = "final_agent_finished"
	eventFinalPatchWritten  = "final_patch_written"
)

type Event struct {
	Sequence     int     `json:"sequence"`
	RunID        string  `json:"run_id"`
	LensID       lens.ID `json:"lens_id,omitempty"`
	Type         string  `json:"type"`
	Attempt      int     `json:"attempt,omitempty"`
	Path         string  `json:"path,omitempty"`
	Status       string  `json:"status,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`
	ChangedFiles int     `json:"changed_files,omitempty"`
	Error        string  `json:"error,omitempty"`
}

type eventRecorder struct {
	mu     sync.Mutex
	path   string
	runID  string
	next   int
	errors []error
}

func newEventRecorder(outDir, runID string) *eventRecorder {
	return &eventRecorder{
		path:  RunEventsPath(outDir),
		runID: runID,
	}
}

func RunEventsPath(outDir string) string {
	return filepath.Join(outDir, "run_events.jsonl")
}

func (recorder *eventRecorder) Path() string {
	if recorder == nil {
		return ""
	}
	return recorder.path
}

func (recorder *eventRecorder) Emit(event Event) {
	if recorder == nil {
		return
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	recorder.next++
	event.Sequence = recorder.next
	event.RunID = recorder.runID
	event.Path = sanitize.Secrets(event.Path)
	event.Status = sanitize.Secrets(event.Status)
	event.Error = sanitize.Secrets(event.Error)

	if err := os.MkdirAll(filepath.Dir(recorder.path), 0o755); err != nil {
		recorder.errors = append(recorder.errors, fmt.Errorf("create run event directory: %w", err))
		return
	}

	file, err := os.OpenFile(recorder.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		recorder.errors = append(recorder.errors, fmt.Errorf("open run event log: %w", err))
		return
	}
	defer file.Close()

	payload, err := json.Marshal(event)
	if err != nil {
		recorder.errors = append(recorder.errors, fmt.Errorf("marshal run event: %w", err))
		return
	}
	payload = append(payload, '\n')
	if _, err := file.Write(payload); err != nil {
		recorder.errors = append(recorder.errors, fmt.Errorf("write run event: %w", err))
	}
}

func (recorder *eventRecorder) Errors() []error {
	if recorder == nil {
		return nil
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	errs := make([]error, len(recorder.errors))
	copy(errs, recorder.errors)
	return errs
}
