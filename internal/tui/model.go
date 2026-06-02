// Package tui implements the interactive bubbletea TUI.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/discovery"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/parser"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/runner"
)

const resultItemName = "📋 Result"

// tickMsg fires on the spinner tick.
type tickMsg time.Time

// worktreesReadyMsg carries discovered worktrees.
type worktreesReadyMsg struct {
	worktrees []discovery.Worktree
}

// Config holds TUI configuration.
type Config struct {
	Dir         string
	Jobs        int
	Timeout     int
	NoWorktrees bool
	Repos       []discovery.Repo
	Runner      *runner.Runner
	Ctx         context.Context
	Cancel      context.CancelFunc
	Profile     bool
}

// Model is the bubbletea model.
type Model struct {
	cfg       Config
	repos     []discovery.Repo         // alphabetical
	results   map[string]*repoItem     // keyed by repo name
	listOrder []string                 // repo names in list order (repos + result)
	cursor    int
	filter    string
	filtering bool

	worktrees []discovery.Worktree
	allDone   bool

	previewScroll    int
	previewMaxScroll int
	previewFocused   bool

	// auto-scroll: follow new lines when not manually scrolling preview
	previewAutoScroll bool

	width  int
	height int

	startTime time.Time
	elapsed   time.Duration

	userNavigated bool // user moved cursor manually
	ctrlC         bool // quit was triggered by Ctrl-C
}

type repoItem struct {
	name   string
	branch string
	status parser.Status
	pid    int
	lines  []string // snapshot, updated on statusMsg/lineMsg
	dirty  bool
}

// New creates a new TUI model.
func New(cfg Config) *Model {
	items := make(map[string]*repoItem, len(cfg.Repos))
	order := make([]string, 0, len(cfg.Repos)+1)
	for _, repo := range cfg.Repos {
		items[repo.Name] = &repoItem{
			name:   repo.Name,
			branch: repo.Branch,
			status: parser.StatusQueued,
			dirty:  repo.Dirty,
		}
		if repo.Dirty {
			items[repo.Name].status = parser.StatusSkipped
		}
		order = append(order, repo.Name)
	}
	// Add Result item.
	items[resultItemName] = &repoItem{
		name:   resultItemName,
		branch: "—",
		status: parser.StatusQueued,
	}
	order = append(order, resultItemName)

	return &Model{
		cfg:               cfg,
		repos:             cfg.Repos,
		results:           items,
		listOrder:         order,
		previewAutoScroll: true,
		startTime:         time.Now(),
	}
}

// Init starts background work.
func (model *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd()}
	if !model.cfg.NoWorktrees {
		cmds = append(cmds, discoverWorktreesCmd(model.cfg.Dir))
	}
	// Start the runner — it sends messages via prog.Send() set in main.go.
	model.cfg.Runner.Start(model.cfg.Ctx, model.cfg.Repos)
	return tea.Batch(cmds...)
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func discoverWorktreesCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		worktrees, _ := discovery.FindWorktrees(dir)
		return worktreesReadyMsg{worktrees: worktrees}
	}
}

// Update handles messages.
func (model *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch message := msg.(type) {
	case tea.WindowSizeMsg:
		model.width = message.Width
		model.height = message.Height

	case tickMsg:
		if !model.allDone {
			model.elapsed = time.Since(model.startTime)
		}
		return model, tickCmd()

	case worktreesReadyMsg:
		model.worktrees = message.worktrees
		model.updateResultPreview()

	case runner.LineMsg:
		if item, ok := model.results[message.RepoName]; ok {
			item.lines = model.cfg.Runner.GetResult(message.RepoName).GetLines()
			// If this repo is selected, update scroll position.
			if model.listOrder[model.cursor] == message.RepoName && model.previewAutoScroll {
				model.previewScroll = len(item.lines)
			}
		}

	case runner.StatusMsg:
		if item, ok := model.results[message.RepoName]; ok {
			item.status = message.Status
			if message.PID != 0 {
				item.pid = message.PID
			}
			if result := model.cfg.Runner.GetResult(message.RepoName); result != nil {
				item.lines = result.GetLines()
			}
			// Auto-select first running repo if user hasn't navigated.
			if message.Status == parser.StatusRunning && !model.userNavigated {
				for idx, name := range model.listOrder {
					if name == message.RepoName {
						model.cursor = idx
						break
					}
				}
			}
		}

	case runner.AllDoneMsg:
		model.allDone = true
		model.elapsed = time.Since(model.startTime)
		model.updateResultPreview()
		// Auto-select Result item.
		model.cursor = len(model.listOrder) - 1
		model.previewAutoScroll = true
		model.previewScroll = 0

	case tea.KeyMsg:
		return model.handleKey(message)
	}

	return model, nil
}

func (model *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Filter mode.
	if model.filtering {
		switch msg.String() {
		case "esc":
			model.filtering = false
			model.filter = ""
		case "backspace":
			if len(model.filter) > 0 {
				model.filter = model.filter[:len(model.filter)-1]
			}
		case "enter":
			model.filtering = false
		default:
			if len(msg.Runes) > 0 {
				model.filter += string(msg.Runes)
			}
		}
		return model, nil
	}

	switch msg.String() {
	case "q", "esc":
		model.cfg.Cancel()
		return model, tea.Quit

	case "ctrl+c":
		model.ctrlC = true
		model.cfg.Cancel()
		return model, tea.Quit

	case "j", "down":
		model.userNavigated = true
		visible := model.visibleList()
		if model.cursor < len(visible)-1 {
			model.cursor++
		}
		model.previewAutoScroll = true
		model.previewScroll = 0

	case "k", "up":
		model.userNavigated = true
		if model.cursor > 0 {
			model.cursor--
		}
		model.previewAutoScroll = true
		model.previewScroll = 0

	case "g":
		model.cursor = 0
		model.userNavigated = true

	case "G":
		model.cursor = len(model.visibleList()) - 1
		model.userNavigated = true

	case "tab":
		model.previewFocused = !model.previewFocused

	case "pgup", "ctrl+u":
		if model.previewFocused {
			model.previewAutoScroll = false
			model.previewScroll -= 10
			if model.previewScroll < 0 {
				model.previewScroll = 0
			}
		}

	case "pgdown", "ctrl+d":
		if model.previewFocused {
			model.previewScroll += 10
		}

	case "end":
		model.previewAutoScroll = true
		model.previewScroll = 0

	case "/":
		model.filtering = true
		model.filter = ""

	case "c":
		// Clear log buffer for selected item.
		visible := model.visibleList()
		if model.cursor < len(visible) {
			name := visible[model.cursor]
			if result := model.cfg.Runner.GetResult(name); result != nil {
				result.ClearLines()
			}
			if item, ok := model.results[name]; ok {
				item.lines = nil
			}
		}

	case "r", "enter":
		visible := model.visibleList()
		if model.cursor < len(visible) {
			name := visible[model.cursor]
			if item, ok := model.results[name]; ok && item.status == parser.StatusFailed {
				item.status = parser.StatusQueued
				model.cfg.Runner.Retry(model.cfg.Ctx, name)
			}
		}

	case "R":
		for name, item := range model.results {
			if item.status == parser.StatusFailed {
				item.status = parser.StatusQueued
				model.cfg.Runner.Retry(model.cfg.Ctx, name)
			}
		}
	}

	return model, nil
}

func (model *Model) visibleList() []string {
	if model.filter == "" {
		return model.listOrder
	}
	var filtered []string
	for _, name := range model.listOrder {
		if strings.Contains(strings.ToLower(name), strings.ToLower(model.filter)) {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// View renders the entire TUI.
func (model *Model) View() string {
	if model.width == 0 {
		return "Initializing..."
	}

	totalHeight := model.height
	statusBarHeight := 1
	bodyHeight := totalHeight - statusBarHeight - 2 // borders

	leftWidth := model.leftPaneWidth()
	rightWidth := model.width - leftWidth - 3 // borders

	if rightWidth < 10 {
		rightWidth = 10
	}

	leftPane := model.renderLeftPane(leftWidth, bodyHeight)
	rightPane := model.renderRightPane(rightWidth, bodyHeight)
	statusBar := model.renderStatusBar()

	// Build layout with borders.
	top := "┌" + strings.Repeat("─", leftWidth+2) + "┬" + strings.Repeat("─", rightWidth+2) + "┐"
	divider := "├" + strings.Repeat("─", leftWidth+2) + "┼" + strings.Repeat("─", rightWidth+2) + "┤"
	bottom := "└" + strings.Repeat("─", leftWidth+2) + "┴" + strings.Repeat("─", rightWidth+2) + "┘"

	leftLines := strings.Split(leftPane, "\n")
	rightLines := strings.Split(rightPane, "\n")

	// Pad both to bodyHeight.
	for len(leftLines) < bodyHeight {
		leftLines = append(leftLines, strings.Repeat(" ", leftWidth+2))
	}
	for len(rightLines) < bodyHeight {
		rightLines = append(rightLines, strings.Repeat(" ", rightWidth+2))
	}

	var sb strings.Builder

	// Header row.
	leftHeader := model.renderLeftHeader(leftWidth)
	rightHeader := model.renderRightHeader(rightWidth)
	sb.WriteString(top + "\n")
	sb.WriteString("│ " + padRight(leftHeader, leftWidth) + " │ " + padRight(rightHeader, rightWidth) + " │\n")
	sb.WriteString(divider + "\n")

	// Body rows.
	for i := 0; i < bodyHeight; i++ {
		left := ""
		if i < len(leftLines) {
			left = leftLines[i]
		}
		right := ""
		if i < len(rightLines) {
			right = rightLines[i]
		}
		sb.WriteString("│" + padRight(left, leftWidth+2) + "│" + padRight(right, rightWidth+2) + "│\n")
	}

	// Footer with status bar spanning full width.
	sb.WriteString("├" + strings.Repeat("─", model.width-2) + "┤\n")
	sb.WriteString("│" + padRight(" "+statusBar, model.width-2) + "│\n")
	sb.WriteString(bottom)

	return sb.String()
}

func (model *Model) leftPaneWidth() int {
	maxName := 26
	for _, name := range model.listOrder {
		if len(name) > maxName && name != resultItemName {
			maxName = len(name)
		}
	}
	minWidth := maxName + 2
	if minWidth < 28 {
		minWidth = 28
	}
	// Don't let left pane exceed 45% of total width.
	maxAllowed := model.width * 45 / 100
	if minWidth > maxAllowed {
		minWidth = maxAllowed
	}
	return minWidth
}

func (model *Model) renderLeftHeader(width int) string {
	done := model.countDone()
	total := len(model.repos)
	elapsed := model.elapsed.Truncate(100 * time.Millisecond)
	title := fmt.Sprintf("pull-all-tui · %d/%d · %s", done, total, elapsed)
	return padRight(title, width)
}

func (model *Model) renderRightHeader(width int) string {
	visible := model.visibleList()
	if model.cursor >= len(visible) || len(visible) == 0 {
		return ""
	}
	name := visible[model.cursor]
	item := model.results[name]
	if item == nil {
		return name
	}
	pidStr := "—"
	if item.pid != 0 {
		pidStr = fmt.Sprintf("%d", item.pid)
	}
	header := fmt.Sprintf("%s · %s · pid %s", name, item.status.String(), pidStr)
	if model.cfg.Profile && name != resultItemName {
		if result := model.cfg.Runner.GetResult(name); result != nil {
			if elapsed := result.LiveElapsed(); elapsed > 0 {
				header += fmt.Sprintf(" · %.2fs", elapsed.Seconds())
			}
		}
	}
	return padRight(header, width)
}

func (model *Model) renderLeftPane(width, height int) string {
	visible := model.visibleList()
	if len(visible) == 0 {
		return ""
	}

	// Determine branch column width.
	repoColWidth := 0
	for _, name := range visible {
		if name != resultItemName && len(name) > repoColWidth {
			repoColWidth = len(name)
		}
	}
	branchColWidth := width - repoColWidth - 4 // icon + space + space + separator
	if branchColWidth < 6 {
		branchColWidth = 6
	}

	// Determine which items are visible given scroll.
	cursorOffset := model.cursor
	startIdx := 0
	if cursorOffset >= height {
		startIdx = cursorOffset - height + 1
	}

	var lines []string
	for idx := startIdx; idx < len(visible) && len(lines) < height; idx++ {
		name := visible[idx]
		item := model.results[name]
		if item == nil {
			continue
		}

		glyph := statusGlyph(item.status)
		branch := truncate(item.branch, branchColWidth)

		var rowStr string
		if name == resultItemName {
			// Separator + result item.
			if len(lines) > 0 && idx > 0 {
				lines = append(lines, " "+strings.Repeat("─", width-2))
			}
			rowStr = " " + glyph + " " + name
		} else {
			rowStr = " " + glyph + " " + padRight(name, repoColWidth) + " " + branch
		}

		rowStr = padRight(rowStr, width+2)

		if idx == model.cursor {
			rowStr = styleSelected.Render(rowStr)
		}
		lines = append(lines, rowStr)
	}

	return strings.Join(lines, "\n")
}

func (model *Model) renderRightPane(width, height int) string {
	visible := model.visibleList()
	if model.cursor >= len(visible) {
		return ""
	}

	name := visible[model.cursor]
	item := model.results[name]
	if item == nil {
		return ""
	}

	var lines []string
	if name == resultItemName {
		lines = strings.Split(model.buildResultPreview(), "\n")
	} else {
		lines = item.lines
	}

	if len(lines) == 0 {
		return ""
	}

	// Wrap lines to width.
	var wrapped []string
	for _, line := range lines {
		if len(line) <= width {
			wrapped = append(wrapped, line)
		} else {
			// Simple wrapping.
			for len(line) > width {
				wrapped = append(wrapped, line[:width])
				line = line[width:]
			}
			if len(line) > 0 {
				wrapped = append(wrapped, line)
			}
		}
	}

	// Scroll: show last `height` lines if auto-scroll, else show from previewScroll.
	total := len(wrapped)
	var viewLines []string
	if model.previewAutoScroll || model.previewScroll == 0 {
		if total <= height {
			viewLines = wrapped
		} else {
			viewLines = wrapped[total-height:]
		}
	} else {
		start := model.previewScroll
		if start >= total {
			start = total - height
		}
		if start < 0 {
			start = 0
		}
		end := start + height
		if end > total {
			end = total
		}
		viewLines = wrapped[start:end]
	}

	result := strings.Join(viewLines, "\n ")
	return " " + result
}

func (model *Model) renderStatusBar() string {
	running := model.countByStatus(parser.StatusRunning)
	done := model.countDone()
	total := len(model.repos)
	elapsed := model.elapsed.Truncate(100 * time.Millisecond)

	filterStr := ""
	if model.filtering {
		filterStr = fmt.Sprintf(" · filter: %s_", model.filter)
	} else if model.filter != "" {
		filterStr = fmt.Sprintf(" · filter: %s", model.filter)
	}

	return fmt.Sprintf("j/k nav · r retry · R retry-failed · q quit · %d jobs · %d/%d done · %d running · %s%s",
		model.cfg.Jobs, done, total, running, elapsed, filterStr)
}

func (model *Model) countByStatus(status parser.Status) int {
	count := 0
	for _, item := range model.results {
		if item.status == status {
			count++
		}
	}
	return count
}

func (model *Model) countDone() int {
	count := 0
	for name, item := range model.results {
		if name == resultItemName {
			continue
		}
		switch item.status {
		case parser.StatusUpdated, parser.StatusUpToDate, parser.StatusSkipped, parser.StatusFailed:
			count++
		}
	}
	return count
}

func (model *Model) updateResultPreview() {
	item := model.results[resultItemName]
	if item == nil {
		return
	}

	var updated, uptodate, skipped, failed []string
	for _, repo := range model.repos {
		repoItem := model.results[repo.Name]
		if repoItem == nil {
			continue
		}
		switch repoItem.status {
		case parser.StatusUpdated:
			updated = append(updated, repo.Name)
		case parser.StatusUpToDate:
			uptodate = append(uptodate, repo.Name)
		case parser.StatusSkipped:
			skipped = append(skipped, repo.Name)
		case parser.StatusFailed:
			failed = append(failed, repo.Name)
		}
	}

	if len(failed) > 0 {
		item.status = parser.StatusFailed
	} else if model.allDone {
		item.status = parser.StatusUpdated
	}
}

func (model *Model) buildResultPreview() string {
	var updated, uptodate, skipped, failed []string
	branchFor := make(map[string]string)

	for _, repo := range model.repos {
		repoItem := model.results[repo.Name]
		if repoItem == nil {
			continue
		}
		branchFor[repo.Name] = repo.Branch
		switch repoItem.status {
		case parser.StatusUpdated:
			updated = append(updated, repo.Name)
		case parser.StatusUpToDate:
			uptodate = append(uptodate, repo.Name)
		case parser.StatusSkipped:
			skipped = append(skipped, repo.Name)
		case parser.StatusFailed:
			failed = append(failed, repo.Name)
		}
	}

	sort.Strings(updated)
	sort.Strings(uptodate)
	sort.Strings(skipped)
	sort.Strings(failed)

	total := len(updated) + len(uptodate) + len(skipped) + len(failed)

	// Compute padding.
	pad := 0
	for _, repo := range model.repos {
		if len(repo.Name) > pad {
			pad = len(repo.Name)
		}
	}
	for _, wt := range model.worktrees {
		if len(wt.RepoName) > pad {
			pad = len(wt.RepoName)
		}
	}

	var sb strings.Builder
	sb.WriteString("🎉 Pull completed!\n")
	sb.WriteString("\n")

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

	if total > 0 {
		sb.WriteString(fmt.Sprintf("   %d total: %s\n", total, strings.Join(parts, ", ")))
	} else {
		sb.WriteString("   Still running...\n")
	}

	writeSection := func(header string, list []string) {
		if len(list) == 0 {
			return
		}
		sb.WriteString("\n")
		sb.WriteString(header + "\n")
		for _, name := range list {
			sb.WriteString(fmt.Sprintf("   - %-*s  %s\n", pad, name, branchFor[name]))
		}
	}

	writeSection("✨ Updated repositories:", updated)
	writeSection("📦 Unchanged repositories:", uptodate)
	writeSection("⚠️  Skipped repositories (uncommitted changes):", skipped)
	writeSection("❌ Failed repositories:", failed)

	if len(model.worktrees) > 0 {
		sb.WriteString("\n")
		sb.WriteString("🌳 Active worktrees:\n")
		for _, wt := range model.worktrees {
			sb.WriteString(fmt.Sprintf("   - %-*s  %s\n", pad, wt.RepoName, wt.Branch))
		}
	}

	return sb.String()
}

func statusGlyph(status parser.Status) string {
	switch status {
	case parser.StatusUpdated:
		return glyphUpdated
	case parser.StatusRunning:
		return glyphRunning
	case parser.StatusQueued:
		return glyphQueued
	case parser.StatusUpToDate:
		return glyphUpToDate
	case parser.StatusSkipped:
		return glyphSkipped
	case parser.StatusFailed:
		return glyphFailed
	default:
		return "?"
	}
}

func padRight(str string, width int) string {
	// Strip ANSI for length calculation.
	visLen := lipgloss.Width(str)
	if visLen >= width {
		return str
	}
	return str + strings.Repeat(" ", width-visLen)
}

// ExitCode returns the appropriate exit code based on final run state.
// 0 = all succeeded, 1 = any failed, 2 = quit mid-run, 130 = Ctrl-C.
func (model *Model) ExitCode() int {
	if model.ctrlC {
		return 130
	}
	if !model.allDone {
		return 2 // quit mid-run via q/Esc
	}
	for name, item := range model.results {
		if name == resultItemName {
			continue
		}
		if item.status == parser.StatusFailed {
			return 1
		}
	}
	return 0
}

func truncate(str string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(str) <= maxLen {
		return str
	}
	if maxLen <= 1 {
		return "…"
	}
	return str[:maxLen-1] + "…"
}
