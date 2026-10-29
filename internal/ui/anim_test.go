package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestAnimTicksOnlyWhereSomethingMoves(t *testing.T) {
	a := demoApp(t, 100, 30)
	a.step, a.animOn = a.index("Welcome"), false
	if a.animate() == nil {
		t.Fatal("Welcome's logo should tick")
	}
	if a.animate() != nil {
		t.Error("a second tick while one is on its way")
	}
	a.animOn = false
	a.step = a.index("Manage")
	if a.animate() != nil {
		t.Error("Manage doesn't move, yet it ticks")
	}
}

func TestFlowBar(t *testing.T) {
	defer func(f int, p termenv.Profile) { animFrame = f; lipgloss.SetColorProfile(p) }(animFrame, lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.TrueColor)
	animFrame = 0
	still := flowBar(0.5, 20, true)
	if still != flowBar(0.5, 20, false) {
		t.Error("frame 0 should be the still picture")
	}
	animFrame = 8
	if flowBar(0.5, 20, true) == still {
		t.Error("the bar doesn't shimmer while running")
	}
	if w := ansi.StringWidth(flowBar(0.3, 17, true)); w != 17 {
		t.Errorf("bar is %d wide, want 17", w)
	}
}
