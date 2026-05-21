// Package parser classifies git pull output into result statuses.
package parser

import "strings"

// Status represents the result classification of a git pull.
type Status int

const (
	StatusQueued   Status = iota
	StatusRunning
	StatusUpToDate
	StatusUpdated
	StatusSkipped
	StatusFailed
)

// String returns the display name for a status.
func (status Status) String() string {
	switch status {
	case StatusQueued:
		return "queued"
	case StatusRunning:
		return "running"
	case StatusUpToDate:
		return "up-to-date"
	case StatusUpdated:
		return "updated"
	case StatusSkipped:
		return "skipped"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Glyph returns the status icon character.
func (status Status) Glyph() string {
	switch status {
	case StatusQueued:
		return "◯"
	case StatusRunning:
		return "◐"
	case StatusUpToDate:
		return "◌"
	case StatusUpdated:
		return "✓"
	case StatusSkipped:
		return "⊘"
	case StatusFailed:
		return "✗"
	default:
		return "?"
	}
}

// ClassifyOutput classifies the output of a git pull command given exit code and output lines.
func ClassifyOutput(exitCode int, lines []string) Status {
	if exitCode != 0 {
		return StatusFailed
	}
	combined := strings.Join(lines, "\n")
	if strings.Contains(combined, "Already up to date") {
		return StatusUpToDate
	}
	return StatusUpdated
}
