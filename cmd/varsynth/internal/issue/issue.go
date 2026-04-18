package issue

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Issue struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Message     string       `json:"message,omitempty"`
	Service     string       `json:"service,omitempty"`
	Environment string       `json:"environment,omitempty"`
	Frames      []StackFrame `json:"stack_frames"`
}

type StackFrame struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Function  string `json:"function,omitempty"`
	Module    string `json:"module,omitempty"`
	InApp     bool   `json:"in_app,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

// Load reads and validates the normalized issue fixture from disk.
func Load(path string) (Issue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Issue{}, fmt.Errorf("read issue file: %w", err)
	}

	var in Issue
	if err := json.Unmarshal(data, &in); err != nil {
		return Issue{}, fmt.Errorf("parse issue file: %w", err)
	}

	if err := in.Validate(); err != nil {
		return Issue{}, err
	}

	return in, nil
}

// Validate checks that the issue fixture contains the minimum data required by the pipeline.
func (i Issue) Validate() error {
	var problems []string

	if strings.TrimSpace(i.ID) == "" {
		problems = append(problems, "id is required")
	}
	if strings.TrimSpace(i.Title) == "" {
		problems = append(problems, "title is required")
	}
	if len(i.Frames) == 0 {
		problems = append(problems, "stack_frames must contain at least one frame")
	}

	for idx, frame := range i.Frames {
		if strings.TrimSpace(frame.File) == "" {
			problems = append(problems, fmt.Sprintf("stack_frames[%d].file is required", idx))
		}
		if frame.Line <= 0 {
			problems = append(problems, fmt.Sprintf("stack_frames[%d].line must be greater than zero", idx))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid issue file: %s", strings.Join(problems, "; "))
	}

	return nil
}
