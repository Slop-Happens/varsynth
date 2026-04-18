package context

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/config"
	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/issue"
	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/repo"
)

const snippetRadius = 3

type Bundle struct {
	RunID       string          `json:"run_id"`
	GeneratedAt time.Time       `json:"generated_at"`
	RepoRoot    string          `json:"repo_root"`
	BaseBranch  string          `json:"base_branch"`
	BaseCommit  string          `json:"base_commit"`
	DirtyState  DirtyState      `json:"dirty_state"`
	TestCommand string          `json:"test_command"`
	Issue       IssueSummary    `json:"issue"`
	StackFrames []MappedFrame   `json:"stack_frames"`
	Snippets    []MappedSnippet `json:"mapped_snippets"`
}

type DirtyState struct {
	Dirty   bool     `json:"dirty"`
	Summary []string `json:"summary,omitempty"`
}

type IssueSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Message     string `json:"message,omitempty"`
	Service     string `json:"service,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type MappedFrame struct {
	Index         int    `json:"index"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	Function      string `json:"function,omitempty"`
	Module        string `json:"module,omitempty"`
	InApp         bool   `json:"in_app,omitempty"`
	ResolvedPath  string `json:"resolved_path,omitempty"`
	Status        string `json:"status"`
	SnippetID     string `json:"snippet_id,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
}

type MappedSnippet struct {
	ID          string   `json:"id"`
	File        string   `json:"file"`
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
	FocusLine   int      `json:"focus_line"`
	SourceLines []string `json:"source_lines"`
}

// Build transforms repo metadata and a normalized issue into the context bundle consumed by later stages.
func Build(cfg config.Config, meta repo.Metadata, iss issue.Issue) (Bundle, error) {
	runID := fmt.Sprintf("%s-%d", sanitizeID(iss.ID), time.Now().UTC().Unix())

	bundle := Bundle{
		RunID:       runID,
		GeneratedAt: time.Now().UTC(),
		RepoRoot:    meta.Root,
		BaseBranch:  meta.BaseBranch,
		BaseCommit:  meta.BaseCommit,
		DirtyState: DirtyState{
			Dirty:   meta.Dirty,
			Summary: meta.DirtySummary,
		},
		TestCommand: cfg.TestCommand,
		Issue: IssueSummary{
			ID:          iss.ID,
			Title:       iss.Title,
			Message:     iss.Message,
			Service:     iss.Service,
			Environment: iss.Environment,
		},
	}

	for idx, frame := range iss.Frames {
		mapped := MappedFrame{
			Index:    idx,
			File:     frame.File,
			Line:     frame.Line,
			Function: frame.Function,
			Module:   frame.Module,
			InApp:    frame.InApp,
			Status:   "unmapped",
		}

		resolved, ok := resolveFramePath(meta.Root, frame.File)
		if !ok {
			mapped.StatusMessage = "file does not resolve within repository"
			bundle.StackFrames = append(bundle.StackFrames, mapped)
			continue
		}

		snippet, err := extractSnippet(resolved, frame.Line)
		if err != nil {
			mapped.ResolvedPath = resolved
			mapped.StatusMessage = err.Error()
			bundle.StackFrames = append(bundle.StackFrames, mapped)
			continue
		}

		snippet.ID = fmt.Sprintf("snippet-%d", idx)
		mapped.ResolvedPath = resolved
		mapped.Status = "mapped"
		mapped.SnippetID = snippet.ID

		bundle.StackFrames = append(bundle.StackFrames, mapped)
		bundle.Snippets = append(bundle.Snippets, snippet)
	}

	return bundle, nil
}

// Write serializes the context bundle as pretty-printed JSON on disk.
func Write(path string, bundle Bundle) error {
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal context bundle: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write context bundle: %w", err)
	}

	return nil
}

// resolveFramePath tries several repo-local path variants for a stack frame and returns the first file that exists.
func resolveFramePath(repoRoot, file string) (string, bool) {
	candidates := []string{
		filepath.Join(repoRoot, filepath.Clean(file)),
	}

	trimmed := strings.TrimLeft(filepath.ToSlash(file), "/")
	if trimmed != file {
		candidates = append(candidates, filepath.Join(repoRoot, filepath.FromSlash(trimmed)))
	}

	if trimmed != "" {
		parts := strings.Split(trimmed, "/")
		for idx := 1; idx < len(parts); idx++ {
			candidates = append(candidates, filepath.Join(repoRoot, filepath.FromSlash(strings.Join(parts[idx:], "/"))))
		}
	}

	for _, candidate := range candidates {
		resolved := filepath.Clean(candidate)
		rel, err := filepath.Rel(repoRoot, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && !info.IsDir() {
			return resolved, true
		}
	}

	return "", false
}

// extractSnippet returns a bounded slice of source lines centered on the requested line number.
func extractSnippet(path string, line int) (MappedSnippet, error) {
	file, err := os.Open(path)
	if err != nil {
		return MappedSnippet{}, fmt.Errorf("open mapped file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := make([]string, 0, line+snippetRadius)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return MappedSnippet{}, fmt.Errorf("scan mapped file: %w", err)
	}

	if line > len(lines) {
		return MappedSnippet{}, fmt.Errorf("mapped line %d exceeds file length %d", line, len(lines))
	}

	start := max(1, line-snippetRadius)
	end := min(len(lines), line+snippetRadius)
	segment := make([]string, 0, end-start+1)
	for idx := start; idx <= end; idx++ {
		segment = append(segment, lines[idx-1])
	}

	return MappedSnippet{
		File:        path,
		StartLine:   start,
		EndLine:     end,
		FocusLine:   line,
		SourceLines: segment,
	}, nil
}

// sanitizeID normalizes an issue identifier into a filesystem-friendly run ID prefix.
func sanitizeID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "/", "-")
	if id == "" {
		return "run"
	}
	return id
}
