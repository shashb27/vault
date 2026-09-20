# Changelog

## 0.3.0-beta.3 — 2026-09-19

- `vault update` prints just the new version number. Verified end to end: install
  one-liner on macOS in a C locale, `update` to a pinned tag and back, checksum matches
  the release's SHA256SUMS.

## 0.3.0-beta.2 — 2026-09-19

- Installers and `vault update` now pick the newest release **including pre-releases**;
  beta.1 looked for a stable "latest" release, which does not exist yet, and failed.
- Install one-liners use `gh api … --raw`, since raw GitHub URLs on a private repo return 404.
- Installers are attached to each release.

## 0.3.0-beta.1 — 2026-09-19

**One binary, every platform.** vault is now a single Go executable for macOS, Windows
and Linux (no bash, python or node needed). Behaviour is identical to 0.2.1: the same
65-check simulation passes against both implementations. `.vault/` layout unchanged.

- **Windows support** (native, not WSL): junction instead of symlink (no admin needed),
  OneDrive sync state from file attributes, automatic pinning of `.vault` with `attrib +P`,
  ANSI colours enabled in the console. **Not yet verified on a real Windows machine** —
  see `tests/windows-checklist.md`.
- Installers: `install.sh` (macOS/Linux) and `install.ps1` (Windows) download the right
  binary from the latest GitHub release via `gh`. `vault update` replaces itself the same way.
- GitHub Actions builds and attaches binaries on every `v*` tag.
- `vault show <name> [N]`: read the last N turns as text before resuming.
- `vault archive <name>` / `vault restore <name>`: keep the list short without deleting.
- `vault members`.
- **Secrets scan**: after each run, the bytes you added to a shared transcript are checked
  for common key shapes (AWS, Anthropic, OpenAI, GitHub, Slack, Google, private key blocks)
  and you're warned to rotate.
- **Lockdown**: `--dangerously-skip-permissions` and `--permission-mode bypassPermissions`
  are refused inside a vault; vault refuses to launch if the shared `.claude/settings*.json`
  sets `defaultMode: bypassPermissions`. `vault doctor` checks this too.
- Lease heartbeat: your lease is refreshed while Claude runs, so long sessions never look free.
- Shared memory conflict copies are detected again (regression from 0.2.0).
- Shared memory lists the members; the resume prompt states that vault content is shared
  among members by agreement. Without this Claude sometimes refused to repeat, to the second
  person, something the first person had told it (seen in testing).
- `vault sessions` shows a SIZE column and warns above 5 MB (slow to sync, heavy to resume).
- Unit tests for encoding (incl. Windows paths), name resolution, transcript parsing,
  leases, secret patterns.
- `legacy/vault.sh` is the 0.2.1 bash implementation, kept as the reference.

## 0.2.1 — 2026-09-19 (bash, not released separately)

Lease heartbeat, memory conflict check, bypass-permissions block, `vault show`, members in
shared memory, SIZE column. Folded into 0.3.0-beta.1.

## 0.2.0 — 2026-09-19

First version meant for people other than the author. Same `.vault/` layout as 0.1, so
old and new clients can share a vault.

**Named sessions**
- `vault new <name>` starts a shared session with a name; names are unique per vault.
- `vault resume <name>` resumes by name, case-insensitive prefix, or id prefix.
  `vault resume` with no argument shows a numbered picker.
- `vault rename <old> <new>`; names set with `/rename` inside Claude are picked up too.
- `vault sessions` shows NAME, STATE (handed off / in use by … / syncing / unknown),
  STARTED-BY, LAST-BY, LAST-ACTIVE. Unnamed sessions show their first prompt.
  `--json` for scripts.
- Names are stored inside the transcript (Claude Code's own `custom-title` record), so
  they travel with the session. Verified on Claude Code 2.1.278.

**Guided flow**
- Bare `vault` says where you are, what's here, who's in what, and what to do next.
  Outside a vault it lists the vaults you've joined.
- Every command ends with a "→ next:" line.
- `vault init` now also joins.
- `vault resume` waits for sync (torn transcript / missing clean-exit marker) up to 90 s,
  explains what it's waiting for, then asks before continuing. Marker-less sessions idle
  for 15+ minutes with no lease are treated as finished. `--now` skips the wait,
  `--steal` takes over a held session.
- `vault doctor`: checks with fixes. Exits 1 when something's wrong.
- `vault conflicts` lists OneDrive conflict copies with the session name and the machine
  they came from; `vault conflicts show <#>` prints the lost turns as text.
- Quiet on healthy runs; warnings only when something is actually wrong.

**Leases are per session** (`.vault/leases/<id>.json`), so two people can work in
different sessions of one vault at the same time. A vault-wide lease from a 0.1 client is
still respected.

**Install**
- `install.sh` links `~/.local/bin/vault`, fixes PATH, removes an old `alias vault=`.
- `vault update` pulls the latest from git. `vault version`.

**Compatibility**
- `vault claude [--private]` still works (maps to `new` / `private`).
- `vault private` replaces `vault claude --private`.

**Tests**
- `tests/sim.sh`: two simulated users, real Claude calls, 54 checks.

## 0.1 — 2026-07-10

Proof of concept (journal: oxmiq/capsule#1559). `init / join / claude / resume /
sessions / status / leave`, vault-wide lease, handoff markers, torn-transcript refusal,
conflict quarantine, drift warnings, empirical join self-test. Three verification rounds;
see docs/TEST-EVIDENCE.md. In real two-Mac use since 2026-08-27.
