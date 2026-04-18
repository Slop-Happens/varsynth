package sanitize

import (
	"regexp"
	"strings"
)

const redaction = "[REDACTED]"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(bearer\s+)?[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?key|token|secret|password)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{10,}`),
}

// Secrets redacts common credential-shaped values before text is written to
// prompts, candidate artifacts, reports, or logs.
func Secrets(text string) string {
	if text == "" {
		return ""
	}

	redacted := text
	redacted = secretPatterns[0].ReplaceAllString(redacted, `${1}`+redaction)
	redacted = secretPatterns[1].ReplaceAllString(redacted, `${1}`+redaction)
	redacted = secretPatterns[2].ReplaceAllString(redacted, `${1}${2}`+redaction)
	redacted = secretPatterns[3].ReplaceAllString(redacted, redaction)
	return redacted
}

// Log trims a log string to maxBytes and redacts credential-shaped values.
func Log(text string, maxBytes int) string {
	return Secrets(Limit(text, maxBytes))
}

// Limit trims text to maxBytes, preserving a clear truncation marker.
func Limit(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}

	marker := "\n... <truncated>"
	if maxBytes <= len(marker) {
		return marker[:maxBytes]
	}
	return strings.TrimRight(text[:maxBytes-len(marker)], "\n") + marker
}
