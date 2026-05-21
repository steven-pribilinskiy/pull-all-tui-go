package parser_test

import (
	"testing"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/parser"
)

func TestClassifyOutput(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		lines    []string
		want     parser.Status
	}{
		{
			name:     "already up to date",
			exitCode: 0,
			lines:    []string{"Already up to date."},
			want:     parser.StatusUpToDate,
		},
		{
			name:     "already up to date exact casing",
			exitCode: 0,
			lines:    []string{"From github.com:org/repo", "Already up to date"},
			want:     parser.StatusUpToDate,
		},
		{
			name:     "updated with fast-forward",
			exitCode: 0,
			lines: []string{
				"remote: Counting objects: 5, done.",
				"Updating abc1234..def5678",
				"Fast-forward",
				" src/foo.ts | 2 ++",
			},
			want: parser.StatusUpdated,
		},
		{
			name:     "failed non-zero exit",
			exitCode: 1,
			lines:    []string{"error: Your local changes would be overwritten"},
			want:     parser.StatusFailed,
		},
		{
			name:     "failed exit 128",
			exitCode: 128,
			lines:    []string{"fatal: not a git repository"},
			want:     parser.StatusFailed,
		},
		{
			name:     "timeout exit 124",
			exitCode: 124,
			lines:    []string{},
			want:     parser.StatusFailed,
		},
		{
			name:     "empty output exit 0 is updated",
			exitCode: 0,
			lines:    []string{},
			want:     parser.StatusUpdated,
		},
		{
			name:     "multi-line output with already up to date anywhere",
			exitCode: 0,
			lines: []string{
				"From github.com:org/repo",
				" * branch  dev -> FETCH_HEAD",
				"Already up to date",
			},
			want: parser.StatusUpToDate,
		},
		{
			name:     "updated — no already up to date string",
			exitCode: 0,
			lines: []string{
				"Updating abc..def",
				"Fast-forward",
			},
			want: parser.StatusUpdated,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := parser.ClassifyOutput(testCase.exitCode, testCase.lines)
			if got != testCase.want {
				t.Errorf("ClassifyOutput(%d, %v) = %v, want %v",
					testCase.exitCode, testCase.lines, got, testCase.want)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status parser.Status
		want   string
	}{
		{parser.StatusQueued, "queued"},
		{parser.StatusRunning, "running"},
		{parser.StatusUpToDate, "up-to-date"},
		{parser.StatusUpdated, "updated"},
		{parser.StatusSkipped, "skipped"},
		{parser.StatusFailed, "failed"},
	}

	for _, testCase := range tests {
		t.Run(testCase.want, func(t *testing.T) {
			if got := testCase.status.String(); got != testCase.want {
				t.Errorf("Status(%d).String() = %q, want %q", testCase.status, got, testCase.want)
			}
		})
	}
}

func TestStatusGlyph(t *testing.T) {
	tests := []struct {
		status parser.Status
		want   string
	}{
		{parser.StatusQueued, "◯"},
		{parser.StatusRunning, "◐"},
		{parser.StatusUpToDate, "◌"},
		{parser.StatusUpdated, "✓"},
		{parser.StatusSkipped, "⊘"},
		{parser.StatusFailed, "✗"},
	}

	for _, testCase := range tests {
		t.Run(testCase.status.String(), func(t *testing.T) {
			if got := testCase.status.Glyph(); got != testCase.want {
				t.Errorf("Status(%d).Glyph() = %q, want %q", testCase.status, got, testCase.want)
			}
		})
	}
}
