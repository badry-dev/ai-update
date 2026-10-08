# ai-update

A Bubble Tea TUI that updates every AI/coding CLI you have installed, one at a time, with live output. It runs on Linux, WSL, and Windows.

```sh
ai-update                 # pick tools, press enter, watch
ai-update --config FILE   # use a different config
```

- **Config:** written on first run. Add a tool with `name`, `binary`, `version_cmd`, `update_cmd`, `needs_sudo`. Tools whose binary isn't on PATH are hidden.
  - Linux and WSL: `~/.config/ai-update/config.yaml` (`$XDG_CONFIG_HOME`)
  - Windows: `%AppData%\ai-update\config.yaml`
- **Shell:** Linux and WSL run commands with `bash -c`. Windows runs them with `powershell.exe -NoProfile -NonInteractive -Command`. Stdin is closed, so commands must be non-interactive.
- **Sudo:** on Linux and WSL, a selected tool with `needs_sudo: true` asks for your password once and keeps it alive until the run ends. Its command should use `sudo -n`. Windows ignores `needs_sudo`.
- **Logs:** one file per run.
  - Linux and WSL: `~/.local/state/ai-update/<timestamp>.log` (`$XDG_STATE_HOME`)
  - Windows: `%LocalAppData%\ai-update\<timestamp>.log`
- **Keys:** space toggle · `a` all/none · enter start · ↑/↓ choose which tool's output to view · pgup/pgdn scroll · `f` follow the running tool · ctrl+c abort (kills the running update and the processes it started).
- Exits 1 if any update failed or the run was aborted.

The first run writes a default config for the OS you are on. A Linux config that calls `apt-get`, `snap`, or `sudo` will not work in Windows PowerShell; edit the Windows file instead of copying it across.

## Install

```sh
go install github.com/badry-dev/ai-update@latest   # private repo: needs GOPRIVATE=github.com/badry-dev/* and git auth
```

Or from a clone:

```sh
# Linux and WSL
go build -o ai-update . && cp ai-update ~/.local/bin/

# Windows
go build -o ai-update.exe .
```

On Windows, put `ai-update.exe` somewhere on `PATH`.

```sh
go test ./...
```
