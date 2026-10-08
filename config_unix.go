//go:build unix

package main

// Written on first run. Commands run via `bash -c`, stdin is closed, so they
// must be non-interactive. needs_sudo tools should use `sudo -n` — ai-update
// asks for your password once before running them.
const defaultConfig = `# ai-update config — one entry per CLI, updated in this order.
#   binary:      looked up on PATH; tools not found are hidden
#   version_cmd: first x.y.z in its output is shown as the version
#   update_cmd:  run with bash -c, no stdin
#   needs_sudo:  prompt for sudo once before the run (use "sudo -n" in the cmd)
tools:
  - name: claude
    binary: claude
    version_cmd: claude --version
    update_cmd: claude update

  - name: codex
    binary: codex
    version_cmd: codex --version
    update_cmd: sudo -n snap refresh codex
    needs_sudo: true

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

  - name: gh
    binary: gh
    version_cmd: gh --version
    update_cmd: sudo -n apt-get update -qq || echo "apt-get update had errors (see above), trying the install anyway"; sudo -n apt-get install --only-upgrade -y gh
    needs_sudo: true
`
