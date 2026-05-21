// pull-all-tui: interactive multi-repo git-pull dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/unix"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/discovery"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/notty"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/runner"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/tui"
)

const version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	var (
		flagJobs        int
		flagNoTUI       bool
		flagNoWorktrees bool
		flagTimeout     int
		flagVersion     bool
	)

	flag.IntVar(&flagJobs, "jobs", 0, "concurrency (default: nproc)")
	flag.IntVar(&flagJobs, "j", 0, "concurrency (shorthand)")
	flag.BoolVar(&flagNoTUI, "no-tui", false, "force plain streaming output")
	flag.BoolVar(&flagNoWorktrees, "no-worktrees", false, "skip worktree discovery")
	flag.IntVar(&flagTimeout, "timeout", 0, "per-pull timeout in seconds (default: 30)")
	flag.BoolVar(&flagVersion, "version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pull-all-tui [DIR]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flagVersion {
		fmt.Printf("pull-all-tui %s\n", version)
		return 0
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}

	// Resolve abs path.
	if dir == "." {
		if absDir, err := os.Getwd(); err == nil {
			dir = absDir
		}
	}

	// Jobs: flag → env → nproc.
	jobs := flagJobs
	if jobs <= 0 {
		if envJobs := os.Getenv("PULL_JOBS"); envJobs != "" {
			if parsed, err := strconv.Atoi(envJobs); err == nil && parsed > 0 {
				jobs = parsed
			}
		}
	}
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}

	// Timeout: flag → env → 30.
	timeout := flagTimeout
	if timeout <= 0 {
		if envTimeout := os.Getenv("PULL_TIMEOUT"); envTimeout != "" {
			if parsed, err := strconv.Atoi(envTimeout); err == nil && parsed > 0 {
				timeout = parsed
			}
		}
	}
	if timeout <= 0 {
		timeout = 30
	}

	// Determine if we're in a TTY.
	noTUI := flagNoTUI
	if !noTUI {
		if !isTerminal(int(os.Stderr.Fd())) {
			noTUI = true
		}
	}

	if noTUI {
		return notty.Run(notty.Config{
			Dir:         dir,
			Jobs:        jobs,
			Timeout:     timeout,
			NoWorktrees: flagNoWorktrees,
		})
	}

	// TUI mode.
	repos, err := discovery.FindRepos(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use an atomic pointer so we can set the program reference after creation,
	// allowing the runner's Send callback to forward messages to the tea.Program.
	var progPtr atomic.Pointer[tea.Program]

	repoRunner := runner.New(runner.Config{
		Jobs:    jobs,
		Timeout: timeout,
		Send: func(msg interface{}) {
			if prog := progPtr.Load(); prog != nil {
				prog.Send(msg)
			}
		},
	})

	tuiModel := tui.New(tui.Config{
		Dir:         dir,
		Jobs:        jobs,
		Timeout:     timeout,
		NoWorktrees: flagNoWorktrees,
		Repos:       repos,
		Runner:      repoRunner,
		Ctx:         ctx,
		Cancel:      cancel,
	})

	prog := tea.NewProgram(tuiModel,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	progPtr.Store(prog)

	finalModel, runErr := prog.Run()
	cancel()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		return 1
	}

	if finalTUI, ok := finalModel.(*tui.Model); ok {
		return finalTUI.ExitCode()
	}
	return 0
}

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}
