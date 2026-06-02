package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/discovery"
	"github.com/steven-pribilinskiy/pull-all-tui-go/internal/runner"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	repos := []discovery.Repo{
		{Name: "alpha", Branch: "main"},
		{Name: "bravo", Branch: "main", Dirty: true},
		{Name: "charlie", Branch: "main"},
		{Name: "delta", Branch: "feature/long-branch-name"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rnr := runner.New(runner.Config{Jobs: 4, Timeout: 8})
	model := New(Config{Dir: "/tmp", Jobs: 4, Timeout: 8, Repos: repos, Runner: rnr, Ctx: ctx, Cancel: cancel})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model
}

func key(runes ...rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
}

func TestViewRendersAndStatusBarIsTwoRows(t *testing.T) {
	model := newTestModel(t)
	out := model.View()
	if len(out) == 0 {
		t.Fatal("View() returned empty output")
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("View() missing repo list:\n%s", out)
	}
	// Two grouped hotkey rows.
	if !strings.Contains(out, "space result") {
		t.Errorf("status row 1 missing 'space result':\n%s", out)
	}
	if !strings.Contains(out, "drag resize") {
		t.Errorf("status row 2 missing 'drag resize':\n%s", out)
	}
}

func TestSpaceTogglesResultOverlay(t *testing.T) {
	model := newTestModel(t)
	model.cursor = 0 // select alpha
	if model.resultOverlay {
		t.Fatal("overlay should start false")
	}
	model.Update(key(' '))
	if !model.resultOverlay {
		t.Fatal("space should enable overlay")
	}
	name, _ := model.previewName()
	if name != resultItemName {
		t.Fatalf("overlay on: previewName = %q, want Result", name)
	}
	model.Update(key(' '))
	if model.resultOverlay {
		t.Fatal("space again should disable overlay")
	}
	// Navigation clears the overlay.
	model.Update(key(' '))
	model.Update(key('j'))
	if model.resultOverlay {
		t.Fatal("navigation should clear overlay")
	}
}

func TestBracketKeysResizeSplitWithinClamp(t *testing.T) {
	model := newTestModel(t)
	if model.splitRatio != defaultSplit {
		t.Fatalf("default split = %v, want %v", model.splitRatio, defaultSplit)
	}
	for i := 0; i < 40; i++ {
		model.Update(key('['))
	}
	if model.splitRatio < minSplit-1e-9 {
		t.Fatalf("[ underflowed clamp: %v < %v", model.splitRatio, minSplit)
	}
	for i := 0; i < 80; i++ {
		model.Update(key(']'))
	}
	if model.splitRatio > maxSplit+1e-9 {
		t.Fatalf("] overflowed clamp: %v > %v", model.splitRatio, maxSplit)
	}
}

func TestClickSelectsRepoAndClearsOverlay(t *testing.T) {
	model := newTestModel(t)
	model.Update(key(' ')) // enable overlay
	model.View()           // populate geometry (geoRowIndex, geoBodyTop, geoLeftWidth)

	// Body starts at geoBodyTop; row 0 = first repo (alpha). Click row 2 = charlie.
	clickX := model.geoLeftWidth // inside left pane
	clickY := model.geoBodyTop + 2
	model.Update(tea.MouseMsg{X: clickX, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if model.resultOverlay {
		t.Fatal("click should clear overlay")
	}
	if got := model.visibleList()[model.cursor]; got != "charlie" {
		t.Fatalf("click selected %q, want charlie", got)
	}
}

func TestWheelOverLeftPaneMovesSelection(t *testing.T) {
	model := newTestModel(t)
	model.View()
	model.cursor = 0
	leftX := model.geoLeftWidth - 1
	model.Update(tea.MouseMsg{X: leftX, Y: model.geoBodyTop, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if model.cursor != 1 {
		t.Fatalf("wheel down over left pane: cursor = %d, want 1", model.cursor)
	}
	model.Update(tea.MouseMsg{X: leftX, Y: model.geoBodyTop, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if model.cursor != 0 {
		t.Fatalf("wheel up over left pane: cursor = %d, want 0", model.cursor)
	}
}

func TestDragDividerResizes(t *testing.T) {
	model := newTestModel(t)
	model.View()
	dividerCol := model.geoLeftWidth + 3
	// Press on divider, then drag right.
	model.Update(tea.MouseMsg{X: dividerCol, Y: model.geoBodyTop + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !model.dragging {
		t.Fatal("press on divider should start drag")
	}
	model.Update(tea.MouseMsg{X: 60, Y: model.geoBodyTop + 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	wantRatio := clampSplit(float64(60-3) / 100.0)
	if model.splitRatio != wantRatio {
		t.Fatalf("drag set split = %v, want %v", model.splitRatio, wantRatio)
	}
	model.Update(tea.MouseMsg{X: 60, Y: model.geoBodyTop + 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if model.dragging {
		t.Fatal("release should stop drag")
	}
}
