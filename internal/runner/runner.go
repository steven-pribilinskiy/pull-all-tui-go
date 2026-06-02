// Package runner executes git pull for repos concurrently and streams output.
package runner

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/discovery"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/parser"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/profile"
)

const ringBufferCap = 10_000

// RepoResult holds the mutable state for a single repo pull.
type RepoResult struct {
	mu      sync.Mutex
	Repo    discovery.Repo
	Status  parser.Status
	PID     int
	Lines   []string // ring buffer
	lineIdx int      // next write position
	count   int      // total lines written
	Started time.Time
	Elapsed time.Duration
}

// AppendLine adds a line to the ring buffer.
func (result *RepoResult) AppendLine(line string) {
	result.mu.Lock()
	defer result.mu.Unlock()
	if len(result.Lines) < ringBufferCap {
		result.Lines = append(result.Lines, line)
	} else {
		result.Lines[result.lineIdx%ringBufferCap] = line
		result.lineIdx++
	}
	result.count++
}

// GetLines returns a snapshot of lines in order (oldest first).
func (result *RepoResult) GetLines() []string {
	result.mu.Lock()
	defer result.mu.Unlock()
	if len(result.Lines) < ringBufferCap || result.lineIdx == 0 {
		out := make([]string, len(result.Lines))
		copy(out, result.Lines)
		return out
	}
	start := result.lineIdx % ringBufferCap
	out := make([]string, ringBufferCap)
	copy(out, result.Lines[start:])
	copy(out[ringBufferCap-start:], result.Lines[:start])
	return out
}

// LiveElapsed returns the final elapsed if set, else elapsed since start for a
// running pull, else 0.
func (result *RepoResult) LiveElapsed() time.Duration {
	result.mu.Lock()
	defer result.mu.Unlock()
	if result.Elapsed > 0 {
		return result.Elapsed
	}
	if !result.Started.IsZero() {
		return time.Since(result.Started)
	}
	return 0
}

// SetStatus updates the status under the lock.
func (result *RepoResult) SetStatus(status parser.Status) {
	result.mu.Lock()
	defer result.mu.Unlock()
	result.Status = status
}

// GetStatus reads status under the lock.
func (result *RepoResult) GetStatus() parser.Status {
	result.mu.Lock()
	defer result.mu.Unlock()
	return result.Status
}

// ClearLines clears the ring buffer.
func (result *RepoResult) ClearLines() {
	result.mu.Lock()
	defer result.mu.Unlock()
	result.Lines = result.Lines[:0]
	result.lineIdx = 0
	result.count = 0
}

// LineMsg is emitted by the runner when a new line arrives for a repo.
type LineMsg struct {
	RepoName string
	Line     string
}

// StatusMsg is emitted when a repo's status changes.
type StatusMsg struct {
	RepoName string
	Status   parser.Status
	PID      int
}

// AllDoneMsg is emitted when all repos have finished.
type AllDoneMsg struct{}

// Config holds runner configuration.
type Config struct {
	Jobs    int
	Timeout int               // seconds
	Send    func(interface{}) // callback to send messages
}

// Runner manages parallel git pulls.
type Runner struct {
	cfg     Config
	sem     *semaphore.Weighted
	results map[string]*RepoResult
	mu      sync.Mutex
}

// New creates a Runner with the given config.
func New(cfg Config) *Runner {
	return &Runner{
		cfg:     cfg,
		sem:     semaphore.NewWeighted(int64(cfg.Jobs)),
		results: make(map[string]*RepoResult),
	}
}

// GetResult returns the result for a repo by name.
func (runner *Runner) GetResult(name string) *RepoResult {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.results[name]
}

// ProfileRows builds per-repo timing rows for the profile report.
func (runner *Runner) ProfileRows() []profile.Row {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	rows := make([]profile.Row, 0, len(runner.results))
	for _, result := range runner.results {
		result.mu.Lock()
		status := result.Status
		lines := make([]string, len(result.Lines))
		copy(lines, result.Lines)
		rows = append(rows, profile.Row{
			Name:    result.Repo.Name,
			Branch:  result.Repo.Branch,
			Status:  status,
			Elapsed: result.Elapsed,
			LastLog: profile.LastLogLine(status, lines),
		})
		result.mu.Unlock()
	}
	return rows
}

// GetAllResults returns all results in alphabetical order.
func (runner *Runner) GetAllResults() []*RepoResult {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	results := make([]*RepoResult, 0, len(runner.results))
	for _, result := range runner.results {
		results = append(results, result)
	}
	return results
}

// Start kicks off pulls for all repos using a goroutine pool.
func (runner *Runner) Start(ctx context.Context, repos []discovery.Repo) {
	// Initialize all results as queued.
	runner.mu.Lock()
	for _, repo := range repos {
		runner.results[repo.Name] = &RepoResult{
			Repo:   repo,
			Status: parser.StatusSkipped,
		}
		if !repo.Dirty {
			runner.results[repo.Name].Status = parser.StatusQueued
		}
	}
	runner.mu.Unlock()

	// Collect dirty repos to notify asynchronously. Sending synchronously here
	// would deadlock: Start runs inside the TUI model's Init(), which bubbletea
	// calls before its event loop drains the (unbuffered) message channel — a
	// blocking Send would hang before the first frame renders (blank screen).
	var dirty []discovery.Repo
	var waitGroup sync.WaitGroup
	for _, repo := range repos {
		if repo.Dirty {
			dirty = append(dirty, repo)
			continue
		}
		waitGroup.Add(1)
		go func(repo discovery.Repo) {
			defer waitGroup.Done()
			if err := runner.sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer runner.sem.Release(1)
			runner.runPull(ctx, repo)
		}(repo)
	}

	if len(dirty) > 0 {
		go func() {
			for _, repo := range dirty {
				runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusSkipped})
			}
		}()
	}

	go func() {
		waitGroup.Wait()
		runner.cfg.Send(AllDoneMsg{})
	}()
}

// Retry re-runs a pull for a single failed repo.
func (runner *Runner) Retry(ctx context.Context, repoName string) {
	runner.mu.Lock()
	result, ok := runner.results[repoName]
	runner.mu.Unlock()
	if !ok {
		return
	}

	go func() {
		if err := runner.sem.Acquire(ctx, 1); err != nil {
			return
		}
		defer runner.sem.Release(1)
		result.ClearLines()
		runner.runPull(ctx, result.Repo)
	}()
}

func (runner *Runner) runPull(ctx context.Context, repo discovery.Repo) {
	runner.mu.Lock()
	result := runner.results[repo.Name]
	runner.mu.Unlock()

	started := time.Now()
	result.mu.Lock()
	result.Started = started
	result.mu.Unlock()
	defer func() {
		result.mu.Lock()
		result.Elapsed = time.Since(started)
		result.mu.Unlock()
	}()

	result.SetStatus(parser.StatusRunning)
	runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusRunning})

	timeoutSec := runner.cfg.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	// Use a cancel context layered on top of the parent context.
	pullCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(pullCtx, "git", "-C", repo.Path, "pull", "--ff-only")
	cmd.Stdin, _ = os.Open(os.DevNull)

	// Mirror the bash reference: capture stdout+stderr to a tempfile, NOT a
	// pipe. Reason: `git pull` over SSH spawns a ControlMaster daemon that
	// inherits the pipe writers and outlives the SIGKILL exec.CommandContext
	// sends on context cancel. With pipes, bufio.Scanner never sees EOF and
	// the whole worker hangs until ControlPersist expires (minutes). With a
	// tempfile, no daemon-held writer keeps anything open. Live streaming
	// during the pull is lost; for a 2–5 s pull batch-emitting at end is the
	// same UX the bash reference provides.
	logFile, err := os.CreateTemp("", "pull-all-tui-*.log")
	if err != nil {
		result.AppendLine(fmt.Sprintf("error: %v", err))
		result.SetStatus(parser.StatusFailed)
		runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusFailed})
		return
	}
	defer os.Remove(logFile.Name())
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		result.AppendLine(fmt.Sprintf("error: %v", err))
		result.SetStatus(parser.StatusFailed)
		runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusFailed})
		return
	}

	result.mu.Lock()
	result.PID = cmd.Process.Pid
	result.mu.Unlock()
	runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusRunning, PID: cmd.Process.Pid})

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	logFile.Close()

	// Read the captured log and replay it line by line so the UI sees each
	// step (matches the original streaming behavior, just deferred to end).
	capturedLines := []string{}
	if f, ferr := os.Open(logFile.Name()); ferr == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			capturedLines = append(capturedLines, line)
			result.AppendLine(line)
			runner.cfg.Send(LineMsg{RepoName: repo.Name, Line: line})
		}
		f.Close()
	}

	status := parser.ClassifyOutput(exitCode, capturedLines)

	// For updated repos, append git diff --stat output.
	if status == parser.StatusUpdated {
		diffCmd := exec.CommandContext(ctx, "git", "-C", repo.Path,
			"diff", "--stat", "--color=always", "HEAD@{1}", "HEAD")
		diffOut, diffErr := diffCmd.Output()
		if diffErr == nil && len(diffOut) > 0 {
			for _, line := range strings.Split(strings.TrimRight(string(diffOut), "\n"), "\n") {
				result.AppendLine(line)
				runner.cfg.Send(LineMsg{RepoName: repo.Name, Line: line})
			}
		}
	}

	result.SetStatus(status)
	runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: status, PID: cmd.Process.Pid})
}
