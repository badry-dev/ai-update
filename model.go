package main

import (
	"cmp"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// prog is set in main before Run; jobs use it to stream output lines.
var prog *tea.Program

type status int

const (
	stPending status = iota
	stRunning
	stUpdated
	stCurrent
	stFailed
	stSkipped
)

type screen int

const (
	scSelect screen = iota
	scRun
	scDone
)

const maxLines = 3000

type job struct {
	tool     Tool
	selected bool
	status   status
	before   string
	after    string
	lines    []string
	start    time.Time
	dur      time.Duration
	err      error
}

type sudoMsg struct{ err error }

type model struct {
	ctx    context.Context
	cancel context.CancelFunc
	log    *runLog

	jobs        []*job // detected tools
	missing     []string
	run         []int // indices of jobs selected for this run
	current     int   // index into jobs of the running job, -1 if none
	cursor      int   // select screen: index into jobs; run screen: index into run
	follow      bool  // output pane tracks the running job
	showSession bool  // done screen: output pane is the name art and session stats

	screen  screen
	spin    spinner.Model
	vp      viewport.Model
	w, h    int
	started time.Time
	elapsed time.Duration
	aborted bool
}

// ---- styles

var (
	accent  = lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#A78BFA"}
	muted   = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6B6B6B"}
	green   = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	red     = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	yellow  = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FACC15"}
	cyan    = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#67E8F9"}
	titleSt = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	mutedSt = lipgloss.NewStyle().Foreground(muted)
	boldSt  = lipgloss.NewStyle().Bold(true)
	selSt   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	boxSt   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(0, 1)
)

func newModel(tools []Tool, missing []string, log *runLog) model {
	ctx, cancel := context.WithCancel(context.Background())
	jobs := make([]*job, len(tools))
	for i, t := range tools {
		jobs[i] = &job{tool: t, selected: true}
	}
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(cyan)))
	return model{ctx: ctx, cancel: cancel, log: log, jobs: jobs, missing: missing,
		current: -1, follow: true, spin: sp, vp: viewport.New(80, 10)}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spin.Tick}
	for i, j := range m.jobs {
		cmds = append(cmds, fetchVersion(m.ctx, i, j.tool.VersionCmd))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if m.screen == scRun {
			m.elapsed = time.Since(m.started)
		}
		return m, cmd

	case versionMsg:
		if m.jobs[msg.idx].before == "" {
			m.jobs[msg.idx].before = msg.v
		}
		return m, nil

	case sudoMsg:
		if msg.err != nil {
			for _, i := range m.run {
				if j := m.jobs[i]; j.tool.NeedsSudo {
					j.status, j.err = stFailed, fmt.Errorf("sudo authentication failed")
					j.lines = append(j.lines, "✗ skipped: sudo authentication failed")
					m.log.printf("\n=== %s\n--- FAILED: sudo authentication failed\n", j.tool.Name)
				}
			}
		} else {
			go keepSudoAlive(m.ctx)
		}
		return m, m.startNext()

	case lineMsg:
		j := m.jobs[msg.idx]
		if msg.replace && len(j.lines) > 0 {
			j.lines[len(j.lines)-1] = msg.line
		} else {
			j.lines = append(j.lines, msg.line)
			if len(j.lines) > maxLines {
				j.lines = j.lines[len(j.lines)-maxLines:]
			}
		}
		if m.shownJob() == msg.idx {
			m.refreshOutput()
		}
		return m, nil

	case doneMsg:
		j := m.jobs[msg.idx]
		j.dur, j.after, j.err = time.Since(j.start), msg.after, msg.err
		switch {
		case msg.err != nil:
			j.status = stFailed
			j.lines = append(j.lines, "", "✗ "+msg.err.Error())
		case j.after != "" && j.after != j.before:
			j.status = stUpdated
		default:
			j.status = stCurrent
		}
		m.current = -1
		if m.aborted {
			return m, tea.Quit
		}
		return m, m.startNext()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey applies the controls for selecting, running, and inspecting tools.
func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		m.aborted = true
		m.cancel()
		return m, tea.Quit
	}

	switch m.screen {
	case scSelect:
		switch k.String() {
		case "q", "esc":
			m.aborted = true
			m.cancel()
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.jobs)-1 {
				m.cursor++
			}
		case " ", "x":
			if len(m.jobs) > 0 {
				m.jobs[m.cursor].selected = !m.jobs[m.cursor].selected
			}
		case "a":
			all := !slices.ContainsFunc(m.jobs, func(j *job) bool { return !j.selected })
			for _, j := range m.jobs {
				j.selected = !all
			}
		case "enter":
			return m.beginRun()
		}

	case scRun, scDone:
		switch k.String() {
		case "q":
			if m.screen == scDone {
				return m, tea.Quit
			}
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "f":
			if m.screen == scDone {
				m.showSession = true
				m.layout()
				return m, nil
			}
			m.follow = true
			m.onCursorMove()
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(k) // pgup/pgdn/etc scroll the output pane
			return m, cmd
		}
	}
	return m, nil
}

// beginRun queues selected tools and requests sudo only on supported platforms.
func (m model) beginRun() (tea.Model, tea.Cmd) {
	needSudo := false
	for i, j := range m.jobs {
		if j.selected {
			m.run = append(m.run, i)
			needSudo = needSudo || (j.tool.NeedsSudo && sudoAvailable())
		}
	}
	if len(m.run) == 0 {
		return m, nil
	}
	m.screen, m.cursor, m.started = scRun, 0, time.Now()
	m.layout()
	if needSudo {
		// Temporarily hands the terminal to sudo for the password prompt.
		c := exec.Command("bash", "-c", `sudo -n -v 2>/dev/null || { echo "ai-update: some tools need sudo."; sudo -v; }`)
		return m, tea.ExecProcess(c, func(err error) tea.Msg { return sudoMsg{err} })
	}
	return m, m.startNext()
}

// startNext starts the next queued job or opens the completed session poster.
func (m *model) startNext() tea.Cmd {
	for pos, i := range m.run {
		j := m.jobs[i]
		if j.status != stPending {
			continue
		}
		j.status, j.start, m.current = stRunning, time.Now(), i
		if m.follow {
			m.cursor = pos
		}
		m.refreshOutput()
		return runJob(m.ctx, prog, m.log, i, j.tool)
	}
	m.screen = scDone
	m.showSession = true
	m.elapsed = time.Since(m.started)
	m.cancel() // stops the sudo keepalive
	m.layout()
	return nil
}

// moveCursor steps through the run list. On the done screen the first move
// also leaves the session poster so a single tool can still be inspected.
func (m *model) moveCursor(delta int) {
	next := m.cursor + delta
	moved := next >= 0 && next < len(m.run)
	if moved {
		m.cursor = next
	}
	if m.screen == scDone && m.showSession {
		m.showSession = false
		m.layout()
		return
	}
	if moved {
		m.onCursorMove()
	}
}

func (m *model) onCursorMove() {
	if m.current >= 0 {
		m.follow = m.run[m.cursor] == m.current
	}
	m.refreshOutput()
}

func (m model) shownJob() int {
	if m.screen == scSelect || len(m.run) == 0 {
		return -1
	}
	return m.run[m.cursor]
}

// refreshOutput displays the session or selected log while preserving follow mode.
func (m *model) refreshOutput() {
	if m.showSession && m.screen == scDone {
		m.vp.SetContent(m.sessionPoster())
		m.vp.GotoTop()
		return
	}
	idx := m.shownJob()
	if idx < 0 {
		return
	}
	atBottom := m.vp.AtBottom()
	lines := m.jobs[idx].lines
	if len(lines) == 0 {
		lines = []string{mutedSt.Render("(no output yet)")}
	}
	m.vp.SetContent(lipgloss.NewStyle().Width(m.vp.Width).Render(strings.Join(lines, "\n")))
	if atBottom || m.follow {
		m.vp.GotoBottom()
	}
}

// layout sizes the output pane to fill whatever the header and rows leave.
func (m *model) layout() {
	if m.w == 0 {
		return
	}
	used := 1 + 1 + len(m.run) + 1 + 2 + 1 + 1 // title, gap, rows, gap, box border, box title, help
	if m.screen == scDone && !m.showSession {
		used += 2
	}
	m.vp.Width = max(20, m.w-4)
	m.vp.Height = max(3, m.h-used)
	m.refreshOutput()
}

// ---- views

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.screen == scSelect {
		return m.viewSelect()
	}
	return m.viewRun()
}

// viewSelect renders detected tools with the privileges each update requires.
func (m model) viewSelect() string {
	var b strings.Builder
	b.WriteString(titleSt.Render("ai-update") + "  " + mutedSt.Render("choose which CLIs to update") + "\n\n")
	if len(m.jobs) == 0 {
		b.WriteString("  No configured CLIs were found on PATH.\n")
	}
	nameW := m.nameWidth()
	for i, j := range m.jobs {
		cur, box := "  ", mutedSt.Render("[ ]")
		if j.selected {
			box = lipgloss.NewStyle().Foreground(green).Render("[✓]")
		}
		name := fmt.Sprintf("%-*s", nameW, j.tool.Name)
		if i == m.cursor {
			cur, name = selSt.Render("❯ "), selSt.Render(name)
		}
		ver := cmp.Or(j.before, "…")
		sudo := ""
		if j.tool.NeedsSudo && sudoAvailable() {
			sudo = lipgloss.NewStyle().Foreground(yellow).Render(" sudo")
		}
		fmt.Fprintf(&b, "%s%s %s  %-12s %s%s\n", cur, box, name, ver, mutedSt.Render(j.tool.UpdateCmd), sudo)
	}
	if len(m.missing) > 0 {
		b.WriteString("\n" + mutedSt.Render("  not on PATH: "+strings.Join(m.missing, ", ")) + "\n")
	}
	b.WriteString("\n" + mutedSt.Render("  ↑/↓ move · space toggle · a all/none · enter start · q quit"))
	return b.String()
}

// viewRun renders job statuses, the selected output pane, and screen controls.
func (m model) viewRun() string {
	var b strings.Builder
	done := 0
	for _, i := range m.run {
		if s := m.jobs[i].status; s != stPending && s != stRunning {
			done++
		}
	}
	right := mutedSt.Render(fmt.Sprintf("%d/%d  %s", done, len(m.run), fmtDur(m.elapsed)))
	left := titleSt.Render("ai-update")
	b.WriteString(left + strings.Repeat(" ", max(1, m.w-lipgloss.Width(left)-lipgloss.Width(right))) + right + "\n\n")

	nameW := m.nameWidth()
	for pos, i := range m.run {
		j := m.jobs[i]
		icon, detail := m.statusIcon(j)
		cur, name := "  ", fmt.Sprintf("%-*s", nameW, j.tool.Name)
		if pos == m.cursor {
			cur, name = selSt.Render("❯ "), selSt.Render(name)
		}
		dur := ""
		switch j.status {
		case stRunning:
			dur = fmtDur(time.Since(j.start))
		case stUpdated, stCurrent, stFailed:
			dur = fmtDur(j.dur)
		}
		fmt.Fprintf(&b, "%s%s %s  %s %7s  %s\n", cur, icon, name, pad(versions(j), 24), dur, detail)
	}

	if m.screen == scDone && !m.showSession {
		b.WriteString("\n" + m.summary() + "\n")
	}

	shown := m.jobs[m.run[m.cursor]]
	label := " " + shown.tool.Name + " output "
	switch {
	case m.screen == scDone && m.showSession:
		label = " session "
	case m.screen == scDone:
		label += mutedSt.Render("(f: session) ")
	case !m.follow && m.current >= 0:
		label += mutedSt.Render("(f: follow running) ")
	}
	box := boxSt.Width(m.w - 2).Render(m.vp.View())
	// Put the label into the top border line.
	if top, rest, ok := strings.Cut(box, "\n"); ok {
		lw := lipgloss.Width(label)
		if lipgloss.Width(top) > lw+4 {
			top = lipgloss.NewStyle().Foreground(muted).Render("╭─") + boldSt.Render(label) +
				lipgloss.NewStyle().Foreground(muted).Render(strings.Repeat("─", lipgloss.Width(top)-lw-3)+"╮")
			box = top + "\n" + rest
		}
	}
	b.WriteString("\n" + box + "\n")

	help := "↑/↓ select tool · pgup/pgdn scroll · ctrl+c abort"
	if m.screen == scDone {
		help = "↑/↓ inspect output · pgup/pgdn scroll · q quit"
		if !m.showSession {
			help = "↑/↓ inspect output · pgup/pgdn scroll · f session · q quit"
		}
	}
	b.WriteString(mutedSt.Render(help))
	return b.String()
}

func (m model) statusIcon(j *job) (string, string) {
	st := lipgloss.NewStyle()
	switch j.status {
	case stRunning:
		return m.spin.View(), st.Foreground(cyan).Render("updating")
	case stUpdated:
		return st.Foreground(green).Render("✓"), st.Foreground(green).Render("updated")
	case stCurrent:
		return st.Foreground(muted).Render("="), mutedSt.Render("already latest")
	case stFailed:
		return st.Foreground(red).Render("✗"), st.Foreground(red).Render("failed")
	default:
		return mutedSt.Render("·"), mutedSt.Render("queued")
	}
}

func versions(j *job) string {
	before := cmp.Or(j.before, "?")
	switch j.status {
	case stUpdated:
		return before + " → " + lipgloss.NewStyle().Foreground(green).Render(j.after)
	case stRunning:
		return before + " → …"
	}
	return before
}

func (m model) summary() string {
	var up, same, fail int
	for _, i := range m.run {
		switch m.jobs[i].status {
		case stUpdated:
			up++
		case stCurrent:
			same++
		case stFailed:
			fail++
		}
	}
	s := fmt.Sprintf("  Done in %s — %s, %s, %s",
		fmtDur(m.elapsed),
		lipgloss.NewStyle().Foreground(green).Render(fmt.Sprintf("%d updated", up)),
		mutedSt.Render(fmt.Sprintf("%d already latest", same)),
		lipgloss.NewStyle().Foreground(red).Render(fmt.Sprintf("%d failed", fail)))
	return s + "\n" + mutedSt.Render("  log: "+m.log.path)
}

func (m model) nameWidth() int {
	w := 8
	for _, j := range m.jobs {
		w = max(w, len(j.tool.Name))
	}
	return w
}

// pad right-pads to a visible width, ignoring ANSI color codes.
func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
