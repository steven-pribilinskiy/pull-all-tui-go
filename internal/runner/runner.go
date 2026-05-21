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
	Timeout int // seconds
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

	var waitGroup sync.WaitGroup
	for _, repo := range repos {
		if repo.Dirty {
			// Already classified as skipped; emit status immediately.
			runner.cfg.Send(StatusMsg{
				RepoName: repo.Name,
				Status:   parser.StatusSkipped,
			})
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
	// Redirect stdin from /dev/null to prevent SSH ControlMaster hang.
	cmd.Stdin, _ = os.Open(os.DevNull)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		result.AppendLine(fmt.Sprintf("error: %v", err))
		result.SetStatus(parser.StatusFailed)
		runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusFailed})
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		result.AppendLine(fmt.Sprintf("error: %v", err))
		result.SetStatus(parser.StatusFailed)
		runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusFailed})
		return
	}

	if err := cmd.Start(); err != nil {
		result.AppendLine(fmt.Sprintf("error: %v", err))
		result.SetStatus(parser.StatusFailed)
		runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusFailed})
		return
	}

	result.mu.Lock()
	result.PID = cmd.Process.Pid
	result.mu.Unlock()
	runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: parser.StatusRunning, PID: cmd.Process.Pid})

	// Collect all lines for classification.
	var allLines []string
	var linesMu sync.Mutex

	addLine := func(line string) {
		result.AppendLine(line)
		runner.cfg.Send(LineMsg{RepoName: repo.Name, Line: line})
		linesMu.Lock()
		allLines = append(allLines, line)
		linesMu.Unlock()
	}

	var streamWg sync.WaitGroup
	streamWg.Add(2)

	go func() {
		defer streamWg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			addLine(scanner.Text())
		}
	}()

	go func() {
		defer streamWg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			addLine(scanner.Text())
		}
	}()

	streamWg.Wait()
	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	linesMu.Lock()
	capturedLines := make([]string, len(allLines))
	copy(capturedLines, allLines)
	linesMu.Unlock()

	status := parser.ClassifyOutput(exitCode, capturedLines)

	// For updated repos, append git diff --stat output.
	if status == parser.StatusUpdated {
		diffCmd := exec.CommandContext(ctx, "git", "-C", repo.Path,
			"diff", "--stat", "--color=always", "HEAD@{1}", "HEAD")
		diffOut, diffErr := diffCmd.Output()
		if diffErr == nil && len(diffOut) > 0 {
			for _, line := range strings.Split(strings.TrimRight(string(diffOut), "\n"), "\n") {
				addLine(line)
			}
		}
	}

	result.SetStatus(status)
	runner.cfg.Send(StatusMsg{RepoName: repo.Name, Status: status, PID: cmd.Process.Pid})
}
