// Package profile builds the opt-in per-repo timing report.
package profile

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/parser"
)

// Enabled reports whether profiling is on, given the --profile flag and the
// PULL_PROFILE env value (any non-empty value enables it).
func Enabled(flagProfile bool, envProfile string) bool {
	return flagProfile || envProfile != ""
}

// Row is one repo's timing entry in the report. LastLog should be the resolved
// display string, e.g. from LastLogLine.
type Row struct {
	Name    string
	Branch  string
	Status  parser.Status
	Elapsed time.Duration
	LastLog string
}

// statusLabel maps a parser.Status to the fixed report label.
func statusLabel(status parser.Status) string {
	switch status {
	case parser.StatusUpdated:
		return "updated"
	case parser.StatusUpToDate:
		return "uptodate"
	case parser.StatusFailed:
		return "failed"
	case parser.StatusSkipped:
		return "skipped"
	default:
		return strings.ReplaceAll(status.String(), "-", "")
	}
}

// LastLogLine returns the last non-empty line of captured output, trimmed and
// truncated to 100 chars. Falls back to status-specific defaults.
func LastLogLine(status parser.Status, lines []string) string {
	for idx := len(lines) - 1; idx >= 0; idx-- {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed != "" {
			return truncate(trimmed, 100)
		}
	}
	switch status {
	case parser.StatusSkipped:
		return "uncommitted changes"
	case parser.StatusUpToDate:
		return "Already up to date."
	default:
		return ""
	}
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max]
}

// Write emits the report (sorted by elapsed descending) to the writer.
func Write(out io.Writer, rows []Row) {
	sorted := make([]Row, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(left, right int) bool {
		return sorted[left].Elapsed > sorted[right].Elapsed
	})

	namePad := 0
	for _, row := range sorted {
		if len(row.Name) > namePad {
			namePad = len(row.Name)
		}
	}

	fmt.Fprintf(out, "pull-all-tui profile — %d repos, slowest first\n", len(sorted))
	for _, row := range sorted {
		elapsed := fmt.Sprintf("%.2fs", row.Elapsed.Seconds())
		fmt.Fprintf(out, "  %8s  %-10s  %-*s  (%s)  %s\n",
			elapsed,
			statusLabel(row.Status),
			namePad, row.Name,
			row.Branch,
			row.LastLog,
		)
	}
}
