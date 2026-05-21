package tui

import "github.com/charmbracelet/lipgloss"

var (
	stylePaneTitle = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1)

	styleSelected = lipgloss.NewStyle().
			Reverse(true)

	styleStatusBar = lipgloss.NewStyle().
			Faint(true).
			Padding(0, 1)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1)

	colorGreen  = lipgloss.Color("2")
	colorRed    = lipgloss.Color("1")
	colorYellow = lipgloss.Color("3")
	colorBlue   = lipgloss.Color("4")
	colorGray   = lipgloss.Color("8")
	colorCyan   = lipgloss.Color("6")

	glyphUpdated  = lipgloss.NewStyle().Foreground(colorGreen).Render("✓")
	glyphRunning  = lipgloss.NewStyle().Foreground(colorBlue).Render("◐")
	glyphQueued   = lipgloss.NewStyle().Foreground(colorGray).Render("◯")
	glyphUpToDate = lipgloss.NewStyle().Foreground(colorCyan).Render("◌")
	glyphSkipped  = lipgloss.NewStyle().Foreground(colorYellow).Render("⊘")
	glyphFailed   = lipgloss.NewStyle().Foreground(colorRed).Render("✗")
)
