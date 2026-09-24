# ai-update

A Bubble Tea TUI that updates every AI/coding CLI installed in WSL, one at a time, with live output.

```sh
ai-update                 # pick tools, press enter, watch
ai-update --config FILE   # use a different config
```

- **Config:** `~/.config/ai-update/config.yaml` (written on first run). Add a tool with `name`, `binary`, `version_cmd`, `update_cmd`, `needs_sudo`. Tools whose binary isn't on PATH are hidden.
- **Sudo:** if a selected tool has `needs_sudo: true`, you're asked for your password once before the run, and it's kept alive until the run ends. Update commands should use `sudo -n`.
- **Logs:** `~/.local/state/ai-update/<timestamp>.log`
- **Keys:** space toggle · `a` all/none · enter start · ↑/↓ choose which tool's output to view · pgup/pgdn scroll · `f` follow the running tool · ctrl+c abort (kills the running update).
- Exits 1 if any update failed or the run was aborted.

## Install

```sh
go install github.com/badry-dev/ai-update@latest   # private repo: needs GOPRIVATE=github.com/badry-dev/* and git auth
```

Or from a clone:

```sh
go build -o ai-update . && cp ai-update ~/.local/bin/
go test ./...
```
