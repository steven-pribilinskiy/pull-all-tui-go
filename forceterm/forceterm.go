// Package forceterm pins lipgloss's color profile and background colour so
// bubbletea never issues a blocking terminal query at startup.
//
// bubbletea's tea_init.go runs `_ = lipgloss.HasDarkBackground()` in an
// init(), which (in non-tmux terminals) fires an OSC 11 background query plus
// a DSR cursor-position terminator and blocks in termenv.waitForData for up to
// termenv.OSCTimeout (5s) waiting for the terminal to answer. Terminals that
// don't answer leave the screen blank for that whole window — the "Go TUI
// renders blank" bug. (Rust/crossterm and Bun/ink never query, so they're
// unaffected — which is exactly the reported symptom.)
//
// Go initializes sibling packages in import-path order, so a package under
// github.com/steven-pribilinskiy/... can't beat github.com/charmbracelet/...
// to the init. This package is imported via the module path "forceterm.local"
// (an "f" path that sorts before "github.com/...") through a local replace,
// guaranteeing its init() runs before bubbletea's. With the background already
// cached, bubbletea's HasDarkBackground() call returns instantly and never
// queries the terminal.
package forceterm

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() {
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
}
