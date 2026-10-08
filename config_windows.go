//go:build windows

package main

// Written on first run. Commands run with powershell.exe, stdin closed, so
// they must be non-interactive. needs_sudo is ignored on Windows.
const defaultConfig = `# ai-update config — one entry per CLI, updated in this order.
#   binary:      looked up on PATH; tools not found are hidden
#   version_cmd: first x.y.z in its output is shown as the version
#   update_cmd:  run with powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command, no stdin
#   needs_sudo:  ignored on Windows
tools:
  - name: claude
    binary: claude
    version_cmd: claude --version
    update_cmd: claude update

  # codex update covers the script and npm installs. A winget install
  # (OpenAI.Codex) is updated with: winget upgrade --id OpenAI.Codex -e
  - name: codex
    binary: codex
    version_cmd: codex --version
    update_cmd: codex update

  - name: opencode
    binary: opencode
    version_cmd: opencode --version
    update_cmd: opencode upgrade

  - name: copilot
    binary: copilot
    version_cmd: copilot --version
    update_cmd: copilot update

  - name: grok
    binary: grok
    version_cmd: grok --version
    update_cmd: grok update

  # -1978335189 is winget's "no applicable upgrade" code: already latest.
  - name: gh
    binary: gh
    version_cmd: gh --version
    update_cmd: "if (-not (Get-Command winget.exe -CommandType Application -ErrorAction SilentlyContinue)) { Write-Error 'winget is required to update gh'; exit 1 }; winget.exe upgrade --id GitHub.cli -e --accept-source-agreements --accept-package-agreements --disable-interactivity; if ($LASTEXITCODE -eq -1978335189) { exit 0 }; exit $LASTEXITCODE"
`
