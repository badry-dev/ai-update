package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type lineMsg struct {
	idx     int
	line    string
	replace bool // overwrite the previous line (a \r progress update)
}

type doneMsg struct {
	idx   int
	after string
	err   error
}

type versionMsg struct {
	idx int
	v   string
}

var (
	versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?([-+][0-9A-Za-z.-]+)?`)
	// Cursor-to-column / erase-line codes that spinners use instead of \r.
	redrawRe = regexp.MustCompile(`\x1b\[\d*[GK]|\x1b\[\d{2,}D`)
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78]`)
)

func bashCmd(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	// Own process group so cancel kills the whole pipeline, not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	// Bounds the wait if a tool leaves a background child holding the pipe open.
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

func getVersion(ctx context.Context, script string) string {
	if script == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, _ := bashCmd(ctx, script).CombinedOutput()
	return versionRe.FindString(ansiRe.ReplaceAllString(string(out), ""))
}

func fetchVersion(ctx context.Context, idx int, script string) tea.Cmd {
	return func() tea.Msg { return versionMsg{idx, getVersion(ctx, script)} }
}

// jobsWG tracks the running update so main can wait for it to be killed on abort.
var jobsWG sync.WaitGroup

// runJob runs one update, streaming output lines to the program and the log.
func runJob(ctx context.Context, p *tea.Program, log *runLog, idx int, t Tool) tea.Cmd {
	jobsWG.Add(1)
	return func() tea.Msg {
		defer jobsWG.Done()
		log.header(t)
		w := &lineWriter{emit: func(line string, replace bool) {
			log.line(line, replace)
			p.Send(lineMsg{idx, line, replace})
		}}
		cmd := bashCmd(ctx, t.UpdateCmd)
		cmd.Stdout, cmd.Stderr = w, w // same writer => Go uses a single pipe, no interleaving races
		err := cmd.Run()
		w.flush()
		if ctx.Err() != nil {
			err = fmt.Errorf("cancelled")
		}
		after := ""
		if ctx.Err() == nil {
			after = getVersion(ctx, t.VersionCmd)
		}
		log.footer(err, after)
		return doneMsg{idx, after, err}
	}
}

// lineWriter splits output on \n and \r, strips ANSI codes, and marks lines
// that redraw a \r-terminated line as replacements so progress bars animate
// in place instead of flooding the pane.
type lineWriter struct {
	buf      bytes.Buffer
	lineOpen bool // last emitted line ended in \r, so the next one overwrites it
	emit     func(line string, replace bool)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	p = redrawRe.ReplaceAll(p, []byte("\r"))
	for _, b := range p {
		switch b {
		case '\n':
			// A bare \n after "text\r" just closes the already-emitted line.
			if w.buf.Len() > 0 || !w.lineOpen {
				w.send()
			}
			w.lineOpen = false
		case '\r':
			if w.buf.Len() > 0 {
				w.send()
				w.lineOpen = true
			}
		default:
			w.buf.WriteByte(b)
		}
	}
	return n, nil
}

func (w *lineWriter) send() {
	line := strings.TrimRight(ansiRe.ReplaceAllString(w.buf.String(), ""), " \t")
	w.buf.Reset()
	w.emit(line, w.lineOpen)
}

func (w *lineWriter) flush() {
	if w.buf.Len() > 0 {
		w.send()
	}
}

// runLog writes the full output of a run to a timestamped file.
type runLog struct {
	mu      sync.Mutex
	f       *os.File
	path    string
	pending *string // last line, held back in case a \r redraw replaces it
}

func logDir() string {
	home, _ := os.UserHomeDir()
	dir := cmp.Or(os.Getenv("XDG_STATE_HOME"), filepath.Join(home, ".local", "state"))
	return filepath.Join(dir, "ai-update")
}

func newRunLog() (*runLog, error) {
	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, time.Now().Format("2006-01-02_15-04-05")+".log")
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &runLog{f: f, path: path}, nil
}

func (l *runLog) printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.flushPending()
		fmt.Fprintf(l.f, format, a...)
	}
}

func (l *runLog) flushPending() {
	if l.pending != nil {
		fmt.Fprintln(l.f, *l.pending)
		l.pending = nil
	}
}

func (l *runLog) header(t Tool) {
	l.printf("\n=== %s  [%s]\n$ %s\n", t.Name, time.Now().Format(time.TimeOnly), t.UpdateCmd)
}

// Progress-bar redraws overwrite the held-back line, so only the final
// frame of each progress bar reaches the log.
func (l *runLog) line(s string, replace bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	if !replace {
		l.flushPending()
	}
	l.pending = &s
}

func (l *runLog) footer(err error, after string) {
	if err != nil {
		l.printf("--- FAILED: %v\n", err)
	} else {
		l.printf("--- ok, version now %s\n", after)
	}
}

func (l *runLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.flushPending()
		l.f.Close()
		l.f = nil
	}
}

// keepSudoAlive refreshes the sudo timestamp until ctx is done.
func keepSudoAlive(ctx context.Context) {
	tick := time.Tick(60 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			_ = exec.Command("sudo", "-n", "-v").Run()
		}
	}
}
