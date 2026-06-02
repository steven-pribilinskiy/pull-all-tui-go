# pull-all-tui (Go)

Interactive multi-repo git-pull dashboard. Pulls every git repository in a
directory in parallel, streams per-repo output live into a two-pane TUI, and
presents a summary with retry support.

## Build

Requires Go 1.22+.

```bash
make build
# produces ./bin/pull-all-tui
```

## Run

```bash
# TUI mode (auto-detects TTY)
./bin/pull-all-tui [DIR]

# Plain streaming output (like the bash reference)
./bin/pull-all-tui --no-tui [DIR]

# Options
  -j, --jobs N       concurrency (default: nproc)
  --no-tui           force plain output even on TTY
  --no-worktrees     skip worktree discovery
  --timeout SEC      per-pull timeout (default: 30)
  --version
```

## Environment variables

| Variable     | Flag equivalent |
|--------------|-----------------|
| `PULL_JOBS`  | `--jobs`        |
| `PULL_TIMEOUT` | `--timeout`   |

## TUI keybindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Next repo |
| `k` / `↑` | Previous repo |
| `g` | Jump to top |
| `G` | Jump to bottom (Result item) |
| `Space` | Toggle the Result summary in the preview without moving selection (any navigation clears it) |
| `[` / `]` | Narrow / widen the left pane |
| `r` / `Enter` | Retry selected failed repo |
| `R` | Retry all failed repos |
| `c` | Clear selected repo's log buffer |
| `Tab` | Toggle focus: list ↔ preview |
| `PgUp` / `Ctrl-U` | Scroll preview up (preview focused) |
| `PgDn` / `Ctrl-D` | Scroll preview down |
| `End` | Resume auto-scroll in preview |
| `/` | Filter list by substring |
| `Esc` | Clear filter |
| `q` | Quit |
| `Ctrl-C` | Quit (exit 130) |

### Mouse

Click a repo row to select it, scroll the wheel over the left pane to move the
selection or over the right pane to scroll the preview, and drag the divider
between the panes to resize. The app captures the mouse while running, so native
terminal text-selection is suspended until you quit.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | All pulls succeeded (updated, up-to-date, or skipped) |
| 1 | At least one pull failed |
| 2 | User quit mid-run |
| 130 | Ctrl-C interrupt |

## Development

```bash
make test    # run tests
make bench   # run benchmarks
make clean   # remove binary
```
