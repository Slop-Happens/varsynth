package validation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Slop-Happens/varsynth/internal/candidate"
)

const (
	DefaultTimeout     = 5 * time.Minute
	DefaultMaxLogBytes = 64 * 1024
)

type Options struct {
	Command     string
	WorkDir     string
	Timeout     time.Duration
	MaxLogBytes int
}

func Run(ctx context.Context, opts Options) candidate.ValidationResult {
	startedAt := time.Now()
	result := candidate.ValidationResult{
		Command: opts.Command,
		Status:  candidate.ValidationFailed,
	}

	command := strings.TrimSpace(opts.Command)
	if command == "" {
		result.Error = "validation command is required"
		result.DurationMS = durationMS(startedAt)
		return result
	}
	if strings.TrimSpace(opts.WorkDir) == "" {
		result.Error = "validation workdir is required"
		result.DurationMS = durationMS(startedAt)
		return result
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxLogBytes := opts.MaxLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = DefaultMaxLogBytes
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Dir = opts.WorkDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result.DurationMS = durationMS(startedAt)
	result.Stdout = limitLog(stdout.String(), maxLogBytes)
	result.Stderr = limitLog(stderr.String(), maxLogBytes)

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.Status = candidate.ValidationTimedOut
		result.TimedOut = true
		result.Error = runCtx.Err().Error()
		if cmd.ProcessState != nil {
			exitCode := cmd.ProcessState.ExitCode()
			result.ExitCode = &exitCode
		}
		return result
	}

	if err == nil {
		exitCode := 0
		result.Status = candidate.ValidationPassed
		result.ExitCode = &exitCode
		return result
	}

	result.Status = candidate.ValidationFailed
	result.Error = err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		result.ExitCode = &exitCode
	}
	return result
}

func durationMS(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}

func limitLog(log string, maxBytes int) string {
	if maxBytes <= 0 || len(log) <= maxBytes {
		return log
	}
	if maxBytes <= len(truncationMarker()) {
		return truncationMarker()[:maxBytes]
	}
	return log[:maxBytes-len(truncationMarker())] + truncationMarker()
}

func truncationMarker() string {
	return fmt.Sprintf("\n... <truncated>")
}
