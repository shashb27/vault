# Changelog

## 0.4.0 — unreleased (branch feature/0.4-handoff-notes)

Chosen by a three-architect design council (adoption, reliability, trust lenses; unanimous in
round 2). Record: journal entries #14a–#14z on oxmiq/capsule#1559.

**Handoff notes — "for whom, what next" travels with the session.**
- At a clean exit vault asks one line: `@sam check the totals in section 3`. It is stored in
  the existing `.vault/handoff/<id>.done` marker as two optional fields, `to` and `note`.
  Schema 1 unchanged; old clients ignore the fields and rewrite the marker at their own exit.
- Bare `vault` opens with **Waiting for you** when a marker's `to` matches your login, machine
  name, or login@machine. `vault sessions` shows the note under the row; `--json` gains
  `handoff_by/to/note/at`; `show` and `status` format it.
- `vault resume` prints `alex left a note for you: "…"` (or `… for sam — carrying on`) as its
  own line, and passes the note to Claude as one descriptive sentence. The note is consumed
  by that resume.
- `vault note <session> [@who] [text] | --clear`, and `--for <who> --note "<text>"` on
  `vault new`/`vault resume`, set a note without a terminal. `@who` resolves case-insensitively
  against members' user, host and user@host, or a prefix that fits exactly one member.
- Notes are capped at 280 characters and refused if they look like a secret.
- Marker conflict copies (`<id>-<Machine>.done`) are filed under `.vault/conflicts/` silently.

**Join hardening.**
- `vault join` shows the shared instruction/permission files *before* its first Claude call.
- The join self-test runs `claude -p --setting-sources user --strict-mcp-config`, so a shared
  hook, env or apiKeyHelper cannot run on a newcomer's first call. Verified on Claude Code
  2.1.280: a planted SessionStart hook fired with plain `claude -p` and not with the flags.
  `--bare` was rejected: it never reads OAuth/keychain, so subscription logins would fail.

**Tests.** 12 new Go tests; sim suite grows from 65 to 94 checks (join hardening J1–J3 and a
handoff-notes section), all guarded so `legacy/vault.sh` still passes its 65.

**Caveat.** The interactive exit prompt is best-effort on Windows until W11 in
`tests/windows-checklist.md` has run on a real machine; `--for/--note` and `vault note` are
the sim-proven paths.

### Roadmap (decided by the council)
- **0.5** — lossless conflict merge on resume (proposal B): re-chain a conflict copy's records
  onto the main transcript with a chain check before and after the write, receipts,
  `vault conflicts merge/undo`, refusal while another member holds the lease (~3.5 days).
- **0.6** — shared-config review gate and per-member append-only journal (proposal C):
  `vault review [--accept]`, risky-key tagging becomes a gate, `vault log`, for the 5–10
  person stage.

## 0.3.2 — 2026-09-23

- The repo is public now. Install is a plain `curl … | bash` (macOS/Linux) or
  `irm … | iex` (Windows); the GitHub CLI is no longer required. `vault update` downloads
  over HTTPS and falls back to `gh` only if that fails.
- Example names and paths in tests and docs are generic (no real usernames or tenant paths).

## 0.3.1 — 2026-09-22

- Docs and the bare `vault` message now start at step 0: how to get a shared OneDrive
  folder that both machines sync, before `init`/`join`. Shorter install one-liners.

## 0.3.0 — 2026-09-21

First cross-platform release: one Go binary for macOS, Windows and Linux, named sessions,
guided flow, controls, installers and self-update. Everything from 0.3.0-beta.1 to beta.3
below. Windows is built and unit-tested but not yet verified on a real Windows machine
(`tests/windows-checklist.md`).

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
