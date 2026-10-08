package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// sessionRunTestModel drives the real update loop without an interactive terminal.
type sessionRunTestModel struct{ model }

// Init supplies the window size used by the completed-screen regressions.
func (m sessionRunTestModel) Init() tea.Cmd {
	return func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }
}

// Update starts selected tools and exits after the production model finishes them.
func (m sessionRunTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.model.Update(msg)
	m.model = next.(model)
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return m, func() tea.Msg { return tea.KeyMsg{Type: tea.KeyEnter} }
	}
	if m.screen == scDone {
		return m, tea.Quit
	}
	return m, cmd
}

// TestWindowsUpdatesOpenSession exercises PowerShell, job messages, logs, and stats.
func TestWindowsUpdatesOpenSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	log := &runLog{f: f, path: path}
	t.Cleanup(log.close)
	m := newModel([]Tool{
		{Name: "updated-tool", UpdateCmd: "Write-Output 'updated output'", VersionCmd: "Write-Output '2.0.0'"},
		{Name: "current-tool", UpdateCmd: "Write-Output 'current output'", VersionCmd: "Write-Output '1.0.0'"},
		{Name: "failed-tool", UpdateCmd: "cmd.exe /c exit 7", VersionCmd: "Write-Output '1.0.0'"},
	}, nil, log)
	t.Cleanup(m.cancel)
	for _, j := range m.jobs {
		j.before = "1.0.0"
	}
	p := tea.NewProgram(sessionRunTestModel{m}, tea.WithContext(t.Context()),
		tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer())
	previous := prog
	prog = p
	t.Cleanup(func() { prog = previous })
	final, err := p.Run()
	if err != nil {
		t.Fatal(err)
	}
	got := final.(sessionRunTestModel).model
	if got.screen != scDone || !got.showSession || got.current != -1 {
		t.Fatalf("completed state: screen = %v, session = %v, current = %d", got.screen, got.showSession, got.current)
	}
	for i, want := range []status{stUpdated, stCurrent, stFailed} {
		if got.jobs[i].status != want {
			t.Errorf("tool %d status = %v, want %v", i, got.jobs[i].status, want)
		}
	}
	plain := ansiRe.ReplaceAllString(got.vp.View(), "")
	for _, label := range []string{"updated", "already latest", "failed", "failed tools"} {
		if labelColumn(plain, label) < 0 {
			t.Errorf("Windows completed viewport hides %s:\n%s", label, plain)
		}
	}
	if !strings.Contains(plain, "failed-tool") {
		t.Fatalf("Windows session lost the failed tool name:\n%s", plain)
	}
	log.close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"updated output", "current output", "FAILED: exit status 7"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("Windows run log missing %q:\n%s", want, data)
		}
	}
}
