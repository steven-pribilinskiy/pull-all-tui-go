// Package notty implements plain streaming output mode (no TUI),
// byte-identical to the bash reference implementation.
package notty

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/discovery"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/parser"
)

// Config holds configuration for no-TTY mode.
type Config struct {
	Dir         string
	Jobs        int
	Timeout     int
	NoWorktrees bool
}

// repoResult holds completed per-repo state for sequential output.
type repoResult struct {
	status parser.Status
	lines  []string // stdout+stderr lines from git pull
	diff   []string // git diff --stat output (updated repos only)
}

// Run executes all pulls and streams output matching the bash reference.
// Returns exit code (0 = all OK, 1 = any failed).
func Run(cfg Config) int {
	fmt.Printf("🔄 Pulling all repositories in %s...\n", filepath.Base(cfg.Dir))

	repos, err := discovery.FindRepos(cfg.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error discovering repos: %v\n", err)
		return 1
	}

	if len(repos) == 0 {
		fmt.Println()
		fmt.Printf("   No git repositories found in %s.\n", filepath.Base(cfg.Dir))
		return 0
	}

	// Launch worktree discovery in background while pulls run.
	var worktrees []discovery.Worktree
	var worktreeWg sync.WaitGroup
	if !cfg.NoWorktrees {
		worktreeWg.Add(1)
		go func() {
			defer worktreeWg.Done()
			worktrees, _ = discovery.FindWorktrees(cfg.Dir)
		}()
	}

	// Buffer results per repo; parallel pulls write to their slot.
	results := make(map[string]*repoResult, len(repos))
	var resultsMu sync.Mutex
	done := make(map[string]chan struct{}, len(repos))

	for _, repo := range repos {
		results[repo.Name] = &repoResult{}
		ch := make(chan struct{})
		done[repo.Name] = ch
		if repo.Dirty {
			results[repo.Name].status = parser.StatusSkipped
			close(ch)
		}
	}

	sem := semaphore.NewWeighted(int64(cfg.Jobs))
	ctx := context.Background()

	for _, repo := range repos {
		if repo.Dirty {
			continue
		}
		go func(repo discovery.Repo) {
			if err := sem.Acquire(ctx, 1); err != nil {
				close(done[repo.Name])
				return
			}
			defer sem.Release(1)
			result := runPull(ctx, cfg.Timeout, repo)
			resultsMu.Lock()
			results[repo.Name] = result
			resultsMu.Unlock()
			close(done[repo.Name])
		}(repo)
	}

	// Flush output in alphabetical order, waiting for each repo.
	for _, repo := range repos {
		<-done[repo.Name]
		result := results[repo.Name]

		switch result.status {
		case parser.StatusSkipped:
			fmt.Printf("⚠️  Skipping %s (has uncommitted changes)\n", repo.Name)

		case parser.StatusUpToDate:
			fmt.Printf("✅ %s\n", repo.Name)

		case parser.StatusUpdated:
			fmt.Printf("✅ %s\n", repo.Name)
			for _, line := range result.diff {
				fmt.Println(line)
			}
			fmt.Println()

		case parser.StatusFailed:
			fmt.Printf("❌ Failed: %s\n", repo.Name)
			for _, line := range result.lines {
				fmt.Printf("   %s\n", line)
			}
			fmt.Println()
		}
	}

	fmt.Println()
	fmt.Println("🎉 Pull completed!")

	var updated, uptodate, skipped, failed []discovery.Repo
	for _, repo := range repos {
		result := results[repo.Name]
		switch result.status {
		case parser.StatusUpdated:
			updated = append(updated, repo)
		case parser.StatusUpToDate:
			uptodate = append(uptodate, repo)
		case parser.StatusSkipped:
			skipped = append(skipped, repo)
		case parser.StatusFailed:
			failed = append(failed, repo)
		}
	}

	total := len(updated) + len(uptodate) + len(skipped) + len(failed)

	var parts []string
	if len(updated) > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", len(updated)))
	}
	if len(uptodate) > 0 {
		parts = append(parts, fmt.Sprintf("%d up-to-date", len(uptodate)))
	}
	if len(skipped) > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", len(skipped)))
	}
	if len(failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(failed)))
	}
	fmt.Println()
	fmt.Printf("   %d total: %s\n", total, strings.Join(parts, ", "))

	// Wait for worktree discovery.
	if !cfg.NoWorktrees {
		worktreeWg.Wait()
	}

	// Compute padding: max name length across all repos and worktrees.
	pad := 0
	for _, repo := range repos {
		if len(repo.Name) > pad {
			pad = len(repo.Name)
		}
	}
	for _, wt := range worktrees {
		if len(wt.RepoName) > pad {
			pad = len(wt.RepoName)
		}
	}

	branchFor := func(name string) string {
		for _, repo := range repos {
			if repo.Name == name {
				return repo.Branch
			}
		}
		return "?"
	}

	printSection := func(header string, list []discovery.Repo) {
		if len(list) == 0 {
			return
		}
		fmt.Println()
		fmt.Println(header)
		for _, repo := range list {
			fmt.Printf("   - %-*s  %s\n", pad, repo.Name, branchFor(repo.Name))
		}
	}

	printSection("✨ Updated repositories:", updated)
	printSection("📦 Unchanged repositories:", uptodate)
	printSection("⚠️  Skipped repositories (uncommitted changes):", skipped)
	printSection("❌ Failed repositories:", failed)

	if len(worktrees) > 0 {
		sort.Slice(worktrees, func(i, j int) bool {
			return worktrees[i].RepoName < worktrees[j].RepoName
		})
		fmt.Println()
		fmt.Println("🌳 Active worktrees:")
		for _, wt := range worktrees {
			fmt.Printf("   - %-*s  %s\n", pad, wt.RepoName, wt.Branch)
		}
	}

	if len(failed) > 0 {
		return 1
	}
	return 0
}

func runPull(ctx context.Context, timeout int, repo discovery.Repo) *repoResult {
	if timeout <= 0 {
		timeout = 30
	}

	pullCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(pullCtx, "git", "-C", repo.Path, "pull", "--ff-only")
	devNull, _ := os.Open(os.DevNull)
	cmd.Stdin = devNull
	defer devNull.Close()

	// Capture to a tempfile, not a pipe. Reason: SSH ControlMaster daemons
	// spawned by `git pull` inherit any pipe writers and outlive the SIGKILL
	// exec.CommandContext sends on context cancel — bufio.Scanner then never
	// sees EOF and streamWg.Wait hangs until ControlPersist expires (minutes).
	// Tempfile output sidesteps the inheritance hazard entirely. Mirrors the
	// bash reference's strategy.
	logFile, err := os.CreateTemp("", "pull-all-tui-*.log")
	if err != nil {
		return &repoResult{status: parser.StatusFailed, lines: []string{err.Error()}}
	}
	defer os.Remove(logFile.Name())
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return &repoResult{status: parser.StatusFailed, lines: []string{err.Error()}}
	}

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	logFile.Close()

	var capturedLines []string
	if f, ferr := os.Open(logFile.Name()); ferr == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			capturedLines = append(capturedLines, scanner.Text())
		}
		f.Close()
	}

	status := parser.ClassifyOutput(exitCode, capturedLines)
	result := &repoResult{status: status, lines: capturedLines}

	// Append diff stat for updated repos.
	if status == parser.StatusUpdated {
		diffCmd := exec.CommandContext(ctx, "git", "-C", repo.Path,
			"diff", "--stat", "--color=always", "HEAD@{1}", "HEAD")
		diffOut, diffErr := diffCmd.Output()
		if diffErr == nil && len(diffOut) > 0 {
			result.diff = strings.Split(strings.TrimRight(string(diffOut), "\n"), "\n")
		}
	}

	return result
}
