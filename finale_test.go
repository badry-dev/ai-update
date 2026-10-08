package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestWideBannerAligned(t *testing.T) {
	if len(wideBanner) != 10 {
		t.Fatalf("banner lines: got %d, want 10", len(wideBanner))
	}
	if wideBannerWidth != 62 {
		t.Fatalf("banner width %d, want 62", wideBannerWidth)
	}
	for i, line := range wideBanner {
		if got := lipgloss.Width(line); got != wideBannerWidth {
			t.Fatalf("line %d width %d, want %d: %q", i, got, wideBannerWidth, line)
		}
		painted := paintBannerLine(line, i, len(wideBanner))
		if got := lipgloss.Width(painted); got != wideBannerWidth {
			t.Fatalf("painted line %d width %d, want %d", i, got, wideBannerWidth)
		}
	}
	joined := strings.Join(wideBanner, "\n")
	for _, glyph := range []string{"███         ███", "██████", "█████████"} {
		if !strings.Contains(joined, glyph) {
			t.Errorf("banner missing %q", glyph)
		}
	}
}

func TestGradientSamplesEnds(t *testing.T) {
	if got := sampleRGB(darkStops, 0); got != darkStops[0] {
		t.Fatalf("t=0: got %v", got)
	}
	if got := sampleRGB(darkStops, 1); got != darkStops[len(darkStops)-1] {
		t.Fatalf("t=1: got %v", got)
	}
	mid := sampleRGB(darkStops, 0.5)
	if mid == darkStops[0] || mid == darkStops[len(darkStops)-1] {
		t.Fatalf("midpoint did not leave the ends: %v", mid)
	}
}

func TestSessionPosterStats(t *testing.T) {
	m := newModel(nil, nil, &runLog{path: "/tmp/session.log"})
	m.vp.Width = 80
	m.elapsed = 65 * time.Second
	m.jobs = []*job{
		{tool: Tool{Name: "claude"}, status: stUpdated, dur: 2 * time.Second},
		{tool: Tool{Name: "codex"}, status: stCurrent, dur: 30 * time.Second},
		{tool: Tool{Name: "gemini"}, status: stFailed, dur: 4 * time.Second},
	}
	m.run = []int{0, 1, 2}

	plain := ansiRe.ReplaceAllString(m.sessionPoster(), "")
	for _, want := range []string{
		"tools", "elapsed", "1m05s", "updated", "already latest", "failed",
		"slowest", "codex", "30.0s", "gemini", "/tmp/session.log",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("poster missing %q\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "ai-update") {
		t.Errorf("wide poster used the compact wordmark:\n%s", plain)
	}
	toolsCol := labelColumn(plain, "tools")
	for _, label := range []string{"elapsed", "updated", "already latest", "failed", "slowest", "failed tools"} {
		if got := labelColumn(plain, label); got != toolsCol {
			t.Errorf("%s starts at column %d, tools at %d\n%s", label, got, toolsCol, plain)
		}
	}
}

func labelColumn(plain, label string) int {
	for _, line := range strings.Split(plain, "\n") {
		if i := strings.Index(line, label+"  "); i >= 0 {
			return i
		}
	}
	return -1
}

func TestSessionPosterNarrow(t *testing.T) {
	m := newModel(nil, nil, &runLog{path: "/tmp/session.log"})
	m.vp.Width = 30
	m.elapsed = time.Second
	m.run = []int{}

	plain := ansiRe.ReplaceAllString(m.sessionPoster(), "")
	if !strings.Contains(plain, "ai-update") {
		t.Fatalf("narrow poster missing wordmark:\n%s", plain)
	}
}

func TestSingleToolSessionOpensLog(t *testing.T) {
	m := newModel([]Tool{{Name: "claude"}}, nil, &runLog{path: "/tmp/session.log"})
	m.jobs[0].status = stUpdated
	m.jobs[0].lines = []string{"claude log line"}
	m.jobs[0].dur = time.Second
	m.run = []int{0}
	m.w, m.h = 100, 40
	m.started = time.Now().Add(-time.Second)

	m.startNext()
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.showSession {
		t.Fatal("down did not open the only tool's output")
	}
	plain := ansiRe.ReplaceAllString(m.vp.View(), "")
	if !strings.Contains(plain, "claude log line") {
		t.Fatalf("missing log:\n%s", plain)
	}
}

func TestDoneShowsSessionUntilInspect(t *testing.T) {
	m := newModel(
		[]Tool{{Name: "claude"}, {Name: "codex"}},
		nil,
		&runLog{path: "/tmp/session.log"},
	)
	m.jobs[0].status = stUpdated
	m.jobs[0].lines = []string{"claude log line"}
	m.jobs[0].dur = time.Second
	m.jobs[1].status = stCurrent
	m.jobs[1].lines = []string{"codex log line"}
	m.jobs[1].dur = 2 * time.Second
	m.run = []int{0, 1}
	m.cursor = 1
	m.w, m.h = 100, 40
	m.started = time.Now().Add(-2 * time.Second)

	m.startNext()
	if m.screen != scDone || !m.showSession {
		t.Fatalf("after the run: screen %v showSession %v", m.screen, m.showSession)
	}
	plain := ansiRe.ReplaceAllString(m.vp.View(), "")
	if strings.Contains(plain, "codex log line") || strings.Contains(plain, "claude log line") {
		t.Fatalf("session view showed tool output:\n%s", plain)
	}
	if !strings.Contains(plain, "updated") {
		t.Fatalf("session view missing stats:\n%s", plain)
	}
	if view := ansiRe.ReplaceAllString(m.View(), ""); !strings.Contains(view, " session ") {
		t.Fatalf("output box was not labeled session:\n%s", view)
	}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.showSession {
		t.Fatal("moving to a tool left the session poster up")
	}
	plain = ansiRe.ReplaceAllString(m.vp.View(), "")
	if !strings.Contains(plain, "claude log line") {
		t.Fatalf("tool inspect missing log:\n%s", plain)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(model)
	if !m.showSession {
		t.Fatal("f did not return to the session poster")
	}
	plain = ansiRe.ReplaceAllString(m.vp.View(), "")
	if strings.Contains(plain, "claude log line") {
		t.Fatalf("session poster still showed the tool log:\n%s", plain)
	}
}
