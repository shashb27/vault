# Architecture (v0.3, Go rewrite)

**Why a rewrite.** v0.2 is bash plus embedded Python. That cannot run natively on Windows,
and neither Python nor Node is guaranteed on a machine that has Claude Code. A single static
binary per platform has no runtime dependency, installs with one download, and updates
itself. Go was chosen over Rust for speed of porting; the logic is small and already
isolated.

**What does not change.** The on-disk `.vault/` layout (schema 1) and every user-facing
behaviour of v0.2.1. `tests/sim.sh` runs unchanged against the Go binary via `VAULT_BIN`, so
port fidelity is verified by the same 60-plus checks that pass on bash.

## Layout

```
cmd/vault/main.go            argument dispatch, exit codes
internal/vault/
  paths.go                   home, Claude projects dir, state dir, realpath, encodePath
  platform_darwin.go         link = symlink; cloud probe = fileproviderctl; cloud root = ~/Library/CloudStorage
  platform_windows.go        link = junction (mklink /J, no admin); cloud probe = file attributes; cloud root = %OneDrive*%
  platform_linux.go          link = symlink; no cloud probe (rclone/onedrive daemons vary); warns
  transcript.go              scan (cwd, custom-title, first prompt), tailValid, lastCwd, turns(), appendTitle
  index.go                   build index (cache by size+mtime), states, resolve(name|prefix|uuid)
  lease.go                   per-session leases, heartbeat goroutine, legacy vault-wide lease
  handoff.go                 markers, upload wait, secrets scan of appended bytes
  drift.go                   CLAUDE.md / settings hash tracking, bypass-permissions lockdown
  registry.go                vaults joined on this machine
  claude.go                  find and exec `claude`; join self-test with nonce
  ui.go                      colours (VT enable on Windows), table, hints
  commands_*.go              one file per command group
internal/vault/testdata/     transcript fixtures for unit tests
tests/sim.sh                 end-to-end two-user simulation (macOS/Linux; drives real claude)
tests/windows-checklist.md   manual verification on a Windows machine
build.sh                     cross-compile matrix → dist/
install.sh, install.ps1      download the right asset with `gh release download`
.github/workflows/release.yml  build + attach binaries on tag push
```

## Platform layer

| Concern | macOS | Windows | Linux |
|---|---|---|---|
| Claude projects dir | `~/.claude/projects` | `%USERPROFILE%\.claude\projects` | `~/.claude/projects` |
| Path encoding | UTF-16 units, non-alphanumeric → `-`, >200 chars truncated + base36 java-hash | same rule applied to the realpath, e.g. `C:\Users\a\OneDrive - Oxmiq Labs\v` → `C--Users-a-OneDrive---Oxmiq-Labs-v` (**unverified on Windows**; join self-test guards it) | same |
| Link projects dir → vault | symlink | directory junction via `cmd /c mklink /J` (no admin or developer mode needed); detected via reparse-point attribute | symlink |
| Is this a synced folder? | under `~/Library/CloudStorage/` | under `%OneDrive%`, `%OneDriveCommercial%` or `%OneDriveConsumer%` | never (warn) |
| Sync state | `fileproviderctl evaluate` (undocumented) | file attributes: `RECALL_ON_DATA_ACCESS` = cloud-only, `PINNED`, `UNPINNED`; no "uploading" signal, so the upload wait is a fixed short grace | none |
| Pin "always keep" | manual (Finder) | `attrib +P` on the `.vault` folder (**to verify**) | n/a |
| OneDrive running | `pgrep -x OneDrive` | `tasklist` for `OneDrive.exe` | n/a |
| Conflict-copy names | `<id>-<Machine>.jsonl` | `<id>-<Machine>.jsonl` (same rule; also `<id>-<Machine>-2`) | same |
| Launch Claude | exec `claude` | exec `claude.exe` / `claude.cmd` via PATHEXT lookup | exec |
| Realpath | `filepath.EvalSymlinks` | `EvalSymlinks` + drive-letter case as Claude reports it in `cwd` (**to verify**) | same |

## Windows plan and what must be verified on a real machine

Cannot be verified from the Mac; `tests/windows-checklist.md` walks through it.

1. `claude -p` at a OneDrive path writes to `%USERPROFILE%\.claude\projects\<enc>`; record the exact `<enc>` and compare with `vault encode <path>`.
2. `vault join` creates a junction and the self-test passes.
3. `vault new`, exit, `vault sessions` on the Mac shows the session with the right name and STARTED-BY.
4. Mac → Windows handoff and back with the codeword test.
5. OneDrive attributes: cloud-only file detected as not materialised; `attrib +P` pins.
6. Colours render in Windows Terminal and PowerShell; no stray escape codes in cmd.exe.
7. `vault update` replaces a running `vault.exe` (rename-then-replace).

## Release and install

- Tag `vX.Y.Z` → GitHub Actions builds `vault-darwin-arm64`, `vault-darwin-amd64`,
  `vault-windows-amd64.exe`, `vault-windows-arm64.exe`, `vault-linux-amd64`, attaches them to
  a release. Pre-releases (`-beta.N`) from the `next` branch for the beta group.
- `install.sh` / `install.ps1` use `gh release download` (private repo needs auth anyway),
  place the binary in `~/.local/bin` or `%LOCALAPPDATA%\vault`, and fix PATH.
- `vault update` downloads the newest release asset for its own platform and swaps itself.

## Testing

- `go test ./...`: encodePath (mac, Windows, unicode, >200 chars), resolve, index states,
  lease expiry, secrets patterns, conflict-name parsing. Pure functions on fixtures.
- `tests/sim.sh`: unchanged end-to-end suite, run with `VAULT_BIN=dist/vault-darwin-arm64`.
- Windows: manual checklist until a Windows CI runner with Claude Code is available.
