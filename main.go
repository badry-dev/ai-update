// ai-update: a TUI that updates every installed AI/coding CLI, one at a time.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	cfgPath := flag.String("config", configPath(), "path to config `file`")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "ai-update: unexpected argument %q\n\n", flag.Arg(0))
		usage()
		os.Exit(2)
	}

	cfg, created, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-update:", err)
		os.Exit(1)
	}
	if created {
		fmt.Println("ai-update: wrote default config to", *cfgPath)
	}

	var tools []Tool
	var missing []string
	for _, t := range cfg.Tools {
		if _, err := exec.LookPath(t.Binary); err == nil {
			tools = append(tools, t)
		} else {
			missing = append(missing, t.Name)
		}
	}

	log, err := newRunLog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-update: cannot create log:", err)
		os.Exit(1)
	}

	prog = tea.NewProgram(newModel(tools, missing, log), tea.WithAltScreen())
	final, err := prog.Run()
	m := final.(model)
	m.cancel() // on abort, kills the running update's process group
	waitTimeout(&jobsWG, 5*time.Second)
	log.close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-update:", err)
		os.Exit(1)
	}

	// Leave a plain summary in the terminal after the alt screen closes.
	if len(m.run) == 0 {
		os.Remove(log.path)
		return
	}
	failed := false
	for _, i := range m.run {
		j := m.jobs[i]
		icon, detail := m.statusIcon(j)
		if j.status == stRunning {
			icon, detail = "✗", "cancelled"
		}
		fmt.Printf("%s %-10s %s %s\n", icon, j.tool.Name, pad(versions(j), 24), detail)
		failed = failed || j.status == stFailed
	}
	if m.aborted {
		fmt.Println("aborted.")
	}
	fmt.Println(mutedSt.Render("log: " + log.path))
	if failed || m.aborted {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(flag.CommandLine.Output(), `ai-update — update all your AI/coding CLIs in one TUI

Usage:
  ai-update [--config file]

Options:
  --config file  config to use (default %s)
  -h, --help     show this help

Keys:
  select screen  ↑/↓ move · space toggle · a all/none · enter start · q quit
  run screen     ↑/↓ view a tool's output · pgup/pgdn scroll · f follow · ctrl+c abort

Logs are written to %s
Exit status is 1 if any update failed or the run was aborted, 2 on bad usage.
`, configPath(), logDir())
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}
