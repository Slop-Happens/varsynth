package prompt

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Slop-Happens/varsynth/internal/lens"
	"github.com/Slop-Happens/varsynth/internal/sanitize"
)

const (
	Version           = "varsynth-prompt-v1"
	maxFrames         = 12
	maxSnippets       = 8
	maxSnippetLines   = 24
	maxSnippetLineLen = 220
)

//go:embed templates/*.md
var templates embed.FS

type Context struct {
	RunID        string
	RepoRoot     string
	BaseBranch   string
	BaseCommit   string
	TestCommand  string
	Dirty        bool
	DirtySummary []string
	Issue        Issue
	StackFrames  []StackFrame
	Snippets     []Snippet
}

type Issue struct {
	ID          string
	Title       string
	Message     string
	Service     string
	Environment string
}

type StackFrame struct {
	Index         int
	File          string
	Line          int
	Function      string
	Module        string
	InApp         bool
	ResolvedPath  string
	Status        string
	SnippetID     string
	StatusMessage string
}

type Snippet struct {
	ID          string
	File        string
	StartLine   int
	EndLine     int
	FocusLine   int
	SourceLines []string
}

type Payload struct {
	Version Versioned       `json:"version"`
	RunID   string          `json:"run_id"`
	Lens    lens.Definition `json:"lens"`
	Text    string          `json:"text"`
}

type Versioned string

func Build(ctx Context, definition lens.Definition) (Payload, error) {
	if definition.ID == "" {
		return Payload{}, fmt.Errorf("lens id is required")
	}

	shared, err := template("shared.md")
	if err != nil {
		return Payload{}, err
	}
	lensInstructions, err := template(string(definition.ID) + ".md")
	if err != nil {
		return Payload{}, err
	}

	var out bytes.Buffer
	writeLine(&out, "# Varsynth Candidate Prompt")
	writeKV(&out, "Prompt Version", Version)
	writeKV(&out, "Run ID", firstNonEmpty(ctx.RunID, "unknown"))
	writeKV(&out, "Lens", fmt.Sprintf("%s (%s)", definition.Name, definition.ID))
	writeBlank(&out)

	writeSection(&out, "Shared Repair Instructions")
	writeParagraph(&out, shared)
	writeBlank(&out)

	writeSection(&out, "Lens Instructions")
	writeParagraph(&out, lensInstructions)
	writeBlank(&out)

	writeSection(&out, "Repository Context")
	writeKV(&out, "Repository", repoLabel(ctx.RepoRoot))
	writeKV(&out, "Base Branch", firstNonEmpty(ctx.BaseBranch, "unknown"))
	writeKV(&out, "Base Commit", firstNonEmpty(ctx.BaseCommit, "unknown"))
	writeKV(&out, "Dirty", fmt.Sprintf("%t", ctx.Dirty))
	if len(ctx.DirtySummary) > 0 {
		writeList(&out, "Dirty Summary", ctx.DirtySummary, 8)
	}
	writeKV(&out, "Validation Command", firstNonEmpty(ctx.TestCommand, "not provided"))
	writeBlank(&out)

	writeSection(&out, "Issue")
	writeKV(&out, "ID", firstNonEmpty(ctx.Issue.ID, "unknown"))
	writeKV(&out, "Title", firstNonEmpty(ctx.Issue.Title, "unknown"))
	writeKV(&out, "Message", firstNonEmpty(ctx.Issue.Message, "not provided"))
	writeKV(&out, "Service", firstNonEmpty(ctx.Issue.Service, "not provided"))
	writeKV(&out, "Environment", firstNonEmpty(ctx.Issue.Environment, "not provided"))
	writeBlank(&out)

	writeSection(&out, "Stack Frames")
	if len(ctx.StackFrames) == 0 {
		writeLine(&out, "- No stack frames were provided.")
	} else {
		for _, frame := range boundedFrames(ctx.StackFrames) {
			writeLine(&out, fmt.Sprintf("- #%d %s:%d %s status=%s snippet=%s", frame.Index, firstNonEmpty(displayPath(ctx.RepoRoot, frame.File), "unknown"), frame.Line, firstNonEmpty(frame.Function, "unknown"), firstNonEmpty(frame.Status, "unknown"), firstNonEmpty(frame.SnippetID, "none")))
			if frame.Module != "" || frame.StatusMessage != "" {
				writeLine(&out, fmt.Sprintf("  details: module=%s message=%s", firstNonEmpty(frame.Module, "none"), firstNonEmpty(frame.StatusMessage, "none")))
			}
		}
		if len(ctx.StackFrames) > maxFrames {
			writeLine(&out, fmt.Sprintf("- ... %d additional frame(s) omitted", len(ctx.StackFrames)-maxFrames))
		}
	}
	writeBlank(&out)

	writeSection(&out, "Mapped Source Snippets")
	if len(ctx.Snippets) == 0 {
		writeLine(&out, "No mapped snippets were available. Inspect the repository before editing.")
	} else {
		for _, snippet := range boundedSnippets(ctx.Snippets) {
			writeLine(&out, fmt.Sprintf("### %s %s:%d-%d focus=%d", firstNonEmpty(snippet.ID, "snippet"), firstNonEmpty(displayPath(ctx.RepoRoot, snippet.File), "unknown"), snippet.StartLine, snippet.EndLine, snippet.FocusLine))
			writeLine(&out, "```")
			for idx, line := range boundedLines(snippet.SourceLines) {
				lineNo := snippet.StartLine + idx
				writeLine(&out, fmt.Sprintf("%4d | %s", lineNo, limitLine(line)))
			}
			if len(snippet.SourceLines) > maxSnippetLines {
				writeLine(&out, fmt.Sprintf("... %d additional line(s) omitted", len(snippet.SourceLines)-maxSnippetLines))
			}
			writeLine(&out, "```")
		}
		if len(ctx.Snippets) > maxSnippets {
			writeLine(&out, fmt.Sprintf("... %d additional snippet(s) omitted", len(ctx.Snippets)-maxSnippets))
		}
	}
	writeBlank(&out)

	writeSection(&out, "Output Expectations")
	writeLine(&out, "- Modify files only inside the candidate worktree.")
	writeLine(&out, "- Keep the final diff focused and reviewable.")
	writeLine(&out, "- Final response must be a JSON object with `rationale`, `root_cause`, `changed_summary`, `validation_notes`, and `confidence` from 0 to 1 or null.")

	text := sanitize.Secrets(strings.TrimRight(out.String(), "\n") + "\n")
	return Payload{
		Version: Versioned(Version),
		RunID:   ctx.RunID,
		Lens:    definition,
		Text:    text,
	}, nil
}

func Dir(outDir string) string {
	return filepath.Join(outDir, "prompts")
}

func Path(outDir string, id lens.ID) string {
	return filepath.Join(Dir(outDir), string(id)+".md")
}

func Write(outDir string, payload Payload) (string, error) {
	if payload.Lens.ID == "" {
		return "", fmt.Errorf("prompt lens id is required")
	}

	promptDir := Dir(outDir)
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		return "", fmt.Errorf("create prompt directory: %w", err)
	}

	path := Path(outDir, payload.Lens.ID)
	text := sanitize.Secrets(payload.Text)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("write prompt artifact: %w", err)
	}
	return path, nil
}

func template(name string) (string, error) {
	payload, err := templates.ReadFile(filepath.ToSlash(filepath.Join("templates", name)))
	if err != nil {
		return "", fmt.Errorf("read prompt template %s: %w", name, err)
	}
	return strings.TrimSpace(string(payload)), nil
}

func repoLabel(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return "unknown"
	}
	return filepath.Base(filepath.Clean(repoRoot))
}

func displayPath(repoRoot, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}

	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot != "" {
		if rel, err := filepath.Rel(filepath.Clean(repoRoot), filepath.Clean(path)); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(filepath.Clean(path))
}

func boundedFrames(frames []StackFrame) []StackFrame {
	frames = append([]StackFrame(nil), frames...)
	sort.SliceStable(frames, func(i, j int) bool {
		return frames[i].Index < frames[j].Index
	})
	if len(frames) > maxFrames {
		return frames[:maxFrames]
	}
	return frames
}

func boundedSnippets(snippets []Snippet) []Snippet {
	snippets = append([]Snippet(nil), snippets...)
	sort.SliceStable(snippets, func(i, j int) bool {
		return snippets[i].ID < snippets[j].ID
	})
	if len(snippets) > maxSnippets {
		return snippets[:maxSnippets]
	}
	return snippets
}

func boundedLines(lines []string) []string {
	if len(lines) > maxSnippetLines {
		return lines[:maxSnippetLines]
	}
	return lines
}

func limitLine(line string) string {
	line = sanitize.Secrets(line)
	if len(line) <= maxSnippetLineLen {
		return line
	}
	return strings.TrimRight(line[:maxSnippetLineLen], " ") + " ... <line truncated>"
}

func writeSection(out *bytes.Buffer, title string) {
	writeLine(out, "## "+title)
}

func writeParagraph(out *bytes.Buffer, paragraph string) {
	for _, line := range strings.Split(strings.TrimSpace(paragraph), "\n") {
		writeLine(out, line)
	}
}

func writeKV(out *bytes.Buffer, key, value string) {
	writeLine(out, fmt.Sprintf("- %s: %s", key, sanitize.Secrets(value)))
}

func writeList(out *bytes.Buffer, key string, values []string, limit int) {
	writeLine(out, "- "+key+":")
	for idx, value := range values {
		if idx >= limit {
			writeLine(out, fmt.Sprintf("  - ... %d additional item(s) omitted", len(values)-limit))
			return
		}
		writeLine(out, "  - "+sanitize.Secrets(value))
	}
}

func writeLine(out *bytes.Buffer, line string) {
	out.WriteString(line)
	out.WriteByte('\n')
}

func writeBlank(out *bytes.Buffer) {
	out.WriteByte('\n')
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
