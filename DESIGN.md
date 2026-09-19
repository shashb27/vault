# Vault POC — Shared Claude Sessions via a Shared Folder

**Status:** v2 (post-critique) · **Date:** 2026-07-09 · **Owner:** Shash Bhaskar
**Journal:** [oxmiq/capsule#1559](https://github.com/oxmiq/capsule/issues/1559)
**Review:** v1 was adversarially reviewed by a 4-lens critique panel (mechanics, OneDrive,
security, PM/UX) — 7 blockers, 12 majors, 13 minors; all incorporated below.

## 1. Summary

A "Vault" is a shared folder (OneDrive for v1) that owns Claude Code session state.
Any user who opens Claude inside the Vault sees the same pool of sessions: they can
**continue a session another user started** (sequential handoff), or run their
**own sessions that remain visible** to everyone else in the Vault.

Session state normally belongs to the *user* (`~/.claude/projects/...`). This POC
re-homes it to the *folder*.

**Honest scope (post-critique): the conversation is shared; the workbench is not.**
What transfers is the full conversational transcript (including subagent/workflow
records). What does not: checkpoints//rewind state, background-task state, prompt
history, per-user settings/MCP/plugins.

## 2. Requirements (from whiteboard, 2026-07-09)

| ID | Requirement | Source |
|----|-------------|--------|
| R1 | A Claude session started in the Vault is common/visible to all Vault users | "This Claude session is common for both users" |
| R2 | A user can continue another user's session with full **conversational** context | "They can continue as if they are 1 session" |
| R3 | Users can run isolated sessions that are still discoverable by others | "They can have separate session as well (isolated) but visible" |

**Decisions (Shash, 2026-07-09):** OneDrive transport (git deferred) · working POC +
design doc · sequential handoff · standalone `playground/vault-poc/`.

Non-goals for v1: simultaneous multi-user typing in one session; sharing credentials
or per-user config; cross-platform (macOS only); vault subfolders as launch points;
transcript integrity/authentication (see §7 trust model).

## 3. Verified mechanics (Claude Code 2.1.170, macOS — re-verified by critique panel)

1. Session transcripts live per user at `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`.
   The sibling `<uuid>/` dirs hold **subagent and workflow transcripts** (these DO
   travel with the project dir). Checkpoint/file-history state does **not** live here —
   it is per-user at `~/.claude/file-history/<uuid>/`, with more per-session state at
   `~/.claude/tasks/<uuid>/` and `~/.claude/session-env/<uuid>` (none of it travels).
2. `<encoded-cwd>` = the **realpath'd** cwd with **every non-alphanumeric UTF-16 code
   unit** replaced by `-`; results over 200 chars are truncated to 200 + `-` +
   base36(abs(java31 hash of the full path)). *(v1 of this doc wrongly claimed only
   `/` and `.` were replaced — disproven by the code-review panel against the actual
   claude 2.1.170 binary; the POC's tests had passed only because the test paths
   contained no other specials. `vault.sh` now replicates the real encoder exactly and
   `vault join` runs an empirical self-test — it launches `claude -p` once with a nonce
   and verifies the transcript containing that nonce landed in the vault's sessions dir
   (i.e. claude actually traversed the symlink), then deletes the throwaway transcript.
   Nonce-scoping makes it immune to concurrent claude activity — an Opus QA pass caught
   the earlier global-diff version false-failing under concurrency.)*
3. `claude --resume <id>` resolves against the encoded dir of the **current cwd**, and
   by default **reuses the same session id and appends to the same `.jsonl`** — verified;
   this is exactly what R2 needs, and User 1 later sees User 2's continuation.
   (`--fork-session` opts out.) Behavior of `--continue` across users is *unverified* —
   it may consult per-user `lastSessionId` in `~/.claude.json`; the wrapper avoids it.
4. `--session-id <uuid>` pins an id (used by tests). Session titles: AI-generated
   titles live inside the `.jsonl` and travel; `-n` custom-name persistence is unverified.
5. Auth: macOS Keychain holds OAuth tokens; no `~/.claude/.credentials.json` on this
   machine. **Narrow claim:** auth tokens at rest are not in the shared dir — but
   *anything a tool prints lands in the shared transcript* (see §7).
6. Per-user `~/.claude.json` holds trust-dialog acceptance, `allowedTools`, MCP
   enablement per project path — none of it travels; U2's first vault launch shows the
   trust dialog.
7. **The encoded project dir also contains per-project auto-memory (`memory/MEMORY.md`)**
   — under the symlink this becomes shared state (see §4a decision).
8. OneDrive Business syncs dot-folders (verified: `.claude/` at share root uploads;
   `.DS_Store` is client-blocklisted — never name vault files `.lock`, `~$*`,
   `desktop.ini`).
9. `fileproviderctl evaluate <path>` (undocumented Apple debug tool, verified working
   from bash) exposes `isUploaded/isUploading/isMostRecentVersionDownloaded/isSyncPaused/
   isKeepDownloaded` — the only scriptable sync signal we have. Caveat: offline it
   reports false-green.
10. **From POC testing (2026-07-09, see TEST-EVIDENCE.md):** `claude -p --resume <id>`
   appends to the same `<id>.jsonl` (no fork) — T4. Bare `claude --resume` **tolerates a
   torn transcript**: it silently skips the malformed tail and continues from stale
   context with exit 0 — silent context loss, which is why `vault resume`'s torn-tail
   refusal is the only tell — T-torn. `claude project purge` on a joined project removes
   **only the local symlink**, not the shared store (purge = broken join, re-join fixes)
   — T-purge. Resume works when the recorded `cwd` no longer exists, and the
   reorientation prompt successfully recovers cross-user file work — T8.

## 4. Architecture

**Chosen mechanism: per-user symlink of the encoded project dir into the Vault.**

```
User 1 (alice)                                User 2 (bob)
~/.claude/projects/                           ~/.claude/projects/
  -Users-alice-…-vault  (symlink) ──┐   ┌──  -Users-bob-…-vault  (symlink)
                                    ▼   ▼
                        <OneDrive>/vault/.vault/sessions/
                          ├── 3f2a….jsonl   (U1's session — R1)
                          ├── 3f2a…/         (subagent/workflow records — travel)
                          ├── 9c81….jsonl   (U2's isolated session — R3)
                          └── memory/        (shared project memory — see §4a)
```

- One-time `vault join` per user/machine. Afterwards **plain `claude` inside the Vault
  Just Works** (matching the whiteboard). Note: the native `/resume` picker is
  owner-blind; ownership display exists only via `vault sessions`.
- Symlinks live in `~/.claude` (outside OneDrive); the vault contains no symlinks.
- Resume across users appends to the same `.jsonl` → bidirectional continuity.

### 4a. Design decision: shared project memory is a FEATURE

`memory/MEMORY.md` inside the shared sessions dir means both users' Claude instances
read/write the **same project memory**. We embrace this deliberately: a vault is a
shared workspace, so shared "what are we working on" memory is on-theme and supports
R2. Consequences (documented, not hidden): memory written by one user is injected as
context into the other's sessions; concurrent memory writes are a OneDrive
conflict-copy risk (`vault status` checks `memory/` for conflict files); personal
auto-memory does not belong in a vault. `vault join` seeds `memory/MEMORY.md` with a
header saying it is shared.

### Vault on-disk layout

```
<vault-root>/
  .claude/settings.json         # SHARED project permission rules (deliberate, see §7)
  CLAUDE.md                     # SHARED project instructions (deliberate, see §7)
  .vault/
    config.json                 # vault id, name, schema version
    users.json                  # user, host, local vault path, joined_at
    sessions/                   # ← shared Claude project dir (symlink target)
    index.json                  # sessionId → owner, last-active, size (avoids bulk downloads)
    handoff/<uuid>.done         # handoff markers: clean-exit signal per session
    conflicts/                  # quarantined OneDrive conflict copies
    lease.json                  # advisory lease
```

### The `vault` CLI (bash, POC)

| Command | Purpose |
|---------|---------|
| `vault init` | Create `.vault/` skeleton in the current folder |
| `vault join` | Back up any existing encoded dir to **local-only** `~/.claude/vault-backups/<enc>.<ts>/`, create symlink, register in `users.json`, print manual pin instruction + verify via `fileproviderctl` |
| `vault claude [args…]` | Pre-flight (OneDrive running? sync paused? conflicts? settings drift?) → lease → launch `claude` at vault root → on exit: write handoff marker, wait for upload, release lease |
| `vault resume [id]` | Same as above but `--resume`; validates transcript last line parses as JSON (torn-sync guard); injects `--append-system-prompt` path-reorientation (see §6) |
| `vault sessions` | List from `index.json` (opens only new/changed transcripts): id, **started-by = creator (first-entry cwd, labeled unverified)**, last-touched-by, last-active, size |
| `vault status [id]` | Symlink health, lease state, **"last received change"** (NOT "freshness" — cannot see un-synced remote data), materialization state, conflict files, CLAUDE.md/settings drift since last run; with `id`: is this session locally present, materialized, clean-handoff marker present? |
| `vault leave [--force]` | Remove symlink, restore backup; refuses while a lease is active unless `--force`. Prints honestly: leaving does NOT withdraw already-shared sessions (synced copies + OneDrive version history persist) |

Pre-existing local sessions for the vault path are **backed up and hidden, never
merged** — publishing sessions created before joining requires explicit consent.

### Handoff lease (advisory) + handoff protocol

`lease.json`: `{user, host, pid, session_hint, acquired_at, ttl_minutes}` (short TTL,
default 60 min). Checked immediately before exec, released on exit via trap.
**It protects against forgetting, not racing** — it rides the same sync latency as
everything else and is trivially forgeable; there is no cross-machine busy signal in
Claude Code itself (live-session registry `~/.claude/sessions/<pid>.json` is local-only),
so the lease is the *only* guard. Refusal messages are self-explanatory (who, when,
how to override with `--steal`).

**Handoff protocol (the etiquette IS part of the design):**
1. U1 exits `claude` → wrapper writes `handoff/<uuid>.done`, blocks until
   `fileproviderctl` shows `isUploading=0`, releases lease.
2. U2 runs `vault status <uuid>` → confirms transcript present, materialized, `.done`
   marker arrived (marker arrival implies transcript sync likely completed).
3. U2 runs `vault resume <uuid>`.

## 5. Alternatives considered

| Option | Verdict | Why |
|--------|---------|-----|
| `CLAUDE_CONFIG_DIR` → vault | ❌ Rejected | Shares settings/history/todos/statsig (and on Linux, credentials); identity bleed; hot-file conflict churn |
| Sync daemon copying dirs | ❌ Rejected | Race-prone double-sync, more moving parts, no benefit over symlink |
| Git transport | ⏸ Deferred | Solves integrity/attribution properly (signed commits = the fix for §7's poisoned-transcript problem) — strong v2 candidate |
| Symlink encoded project dir | ✅ Chosen | Smallest bridge; core UX needs no wrapper. **Shares transcripts + subagent records + project memory** (not "only transcripts") |

## 6. OneDrive constraints & mitigations (post-critique)

| Constraint | Impact | Mitigation |
|-----------|--------|------------|
| Sync latency (sec–min) | Stale/truncated resume | Handoff protocol above; `vault status <id>` gate; reader-side torn-file JSON validation; writer-side upload wait |
| Torn mid-append snapshots | `.jsonl` ends mid-record even with perfect discipline | `vault resume` validates last line parses; refuse with "still syncing — retry"; empirical resume-on-truncated test (T-torn) |
| Conflict copies | Same-id `*-<hostname>.jsonl` visible to the native picker; **loser's turns silently vanish from primary file** | Quarantine conflict-named files to `.vault/conflicts/` on every launch; report which branch lost |
| Dataless files | Resume stalls; `vault sessions` on fresh join = bulk download (real transcripts run 17 MB+) | **No scriptable pin exists on macOS** — join prints manual "Always Keep on This Device" instruction and verifies via `fileproviderctl` (`isKeepDownloaded`); `index.json` avoids opening transcripts; materialize only the transcript being resumed (`cat > /dev/null`) |
| Paused/offline OneDrive | Everything works locally, nothing propagates, zero signal | Pre-flight: warn if OneDrive not running or `isSyncPaused=1`; note offline false-green |
| Symlinks unsyncable | — | Symlinks never inside the vault |
| Per-user absolute paths | **Functional breakage** (not cosmetic): resumed session reuses `/Users/alice/...` paths → ENOENT on Bob's machine | `vault resume` injects `--append-system-prompt "This session moved machines; vault root is now <path>; re-resolve all file paths"`; test T8 exercises cross-user file edit |
| Upload churn / version history | Long sessions → continuous re-upload of multi-MB file; noisy SharePoint versions (harmless; recycle-bin + versions are also the accidental-deletion recovery story) | Keep demo sessions short; document |

## 7. Security & privacy — trust model (rewritten after critique)

**Joining a vault = giving other members shell-adjacent trust.** State it plainly:

1. **Instruction & permission injection (by design, so disclose it):** the vault root
   syncs `CLAUDE.md` (auto-loaded instructions) and `.claude/settings.local.json`
   (permission allows). Any member can change what Claude is told and what it may do
   without prompting **on your machine**. Mitigations: `vault status` diffs both files
   since your last run and warns on drift; README advises never clicking "don't ask
   again" inside a vault; only vault with people you trust to run commands near you.
2. **Poisoned transcripts:** `.jsonl` files are unauthenticated; any member can edit or
   plant sessions. Resuming executes on YOUR machine with YOUR credentials under
   context anyone in the share can have written. `vault sessions` labels owners
   "(unverified)". The deferred git transport is the real fix (signed, attributed commits).
3. **What actually leaks into shared transcripts:** verbatim output of every tool call —
   files read from *anywhere* on the originating machine, env vars, secrets echoed in a
   terminal, personal MCP connector results (mail/calendar/finance), and your
   name/email via injected context. "Auth tokens at rest aren't shared" is the *only*
   safe credential claim. README rules: don't `cat` secrets in a vault session; avoid
   broad Read allows; keep personal connectors out of vault sessions where possible.
4. **Private-session escape hatch:** `--no-session-persistence` **only works with
   `--print`** — it is NOT an interactive escape hatch (v1 design error, corrected).
   The only reliable escape is working outside the vault folder. `vault claude
   --private` launches from a local scratch cwd to make that easy.
5. **Leave ≠ unshare:** sessions already synced to other members and OneDrive version
   history persist after `vault leave`. Said out loud in the command output.
6. **Shared memory (§4a)** is a disclosed feature, not a leak — but personal auto-memory
   habits must change inside a vault.
7. Destructive-op guard: `claude project purge` (or `rm -rf` of the encoded dir) may
   traverse the symlink and destroy the whole team's sessions — tested in T-purge;
   README warning; OneDrive recycle bin is the recovery story.

## 8. Known limitations (v1)

1. **The conversation is shared; the workbench is not:** checkpoints//rewind,
   background-task state, prompt history (up-arrow), per-user settings/MCP/plugins do
   not transfer. Never demo `/rewind` across a handoff.
2. Launch point must be the vault root (subfolders encode differently).
3. Handoff is best-effort serialized; simultaneous appends → conflict copies with
   silent turn loss on the losing branch (quarantined, not merged, in v1).
4. First `claude` launch per user shows the trust dialog; U1's "always allow" grants
   are per-user (`~/.claude.json`) and don't transfer — shared grants belong in
   `<vault>/.claude/settings.json` (with §7's trust caveat).
5. macOS only. `fileproviderctl` is an undocumented debug tool and may change.
6. Handoff latency grows with transcript size.
7. Native `/resume` picker shows no ownership and can see not-yet-quarantined
   conflict copies.

## 9. Test plan

Simulated two-user tests (one machine, two cwds → same sessions dir) prove the
mechanism; **T9 on two real machines is REQUIRED before any demo** — the
OneDrive-specific risks only exist there. Never demo a path that hasn't run once.

**Status 2026-07-09: T1–T8, T-torn, T-purge ALL PASS (simulated). T9 pending a second
person/machine. Full evidence: TEST-EVIDENCE.md.**

| # | Check | Pass criterion |
|---|-------|----------------|
| T1 | init + join symlink health | `vault status` green; symlink resolves; backup landed in local-only `~/.claude/vault-backups/` |
| T2 | U1 session lands in vault | `claude -p --session-id <uuid> "codeword FALCON-42"` at vault root → `<uuid>.jsonl` in `.vault/sessions/` |
| T3 | R1/R3 visibility + attribution | `vault sessions` as U2 lists U1's session, owner=U1 (started-by) |
| T4 | R2 handoff — **same session, bidirectional** | U2 `claude -p --resume <uuid>` answers FALCON-42 **and** appended to the same `<uuid>.jsonl`, no new session file, and U1 can then see U2's turns |
| T5 | Lease | U2 refused while U1 holds lease (message shows who/when/override); `--steal` works; released on exit; **known limit: single-machine test can't exercise lease races** |
| T6 | Plain-`claude` flow | After join, bare `claude -p` in vault root writes to `.vault/sessions/` |
| T7 | leave restores | Symlink removed, original dir restored, honest unshare warning printed |
| T8 | Cross-user file work (path breakage) | U2 resumes U1's session that created a file, successfully edits it (reorientation prompt) |
| T-torn | Torn transcript | `--resume` against a copy truncated mid-line: record actual behavior; `vault resume` refuses invalid tail |
| T-purge | Purge safety | `claude project purge`-equivalent against symlinked dir: record behavior; README warning matches reality |
| T9 (2 machines) | Real OneDrive dry run | `.vault` dot-folder syncs; jsonl arrives + materializes; `.done` marker arrives; resume succeeds; measure lease + transcript propagation time |

## 10. Rollout / demo script (with sync gates)

1. Shash: `vault init` + `join` in a shared OneDrive folder; `vault claude`; short
   session that states a fact and creates one file; exit cleanly (wrapper writes
   handoff marker + waits for upload).
2. **GATE:** second user runs `vault status <uuid>` and proceeds only when transcript +
   `.done` marker are local and materialized.
3. Second user: `vault join` (expect trust dialog — say so out loud), `vault sessions`
   (owner attribution), `vault resume <uuid>`, ask for the fact, then edit the file U1
   created (proves R2 beyond Q&A).
4. R3: second user starts an isolated session; Shash lists it with `vault sessions`.
5. Do NOT demo: `/rewind` across handoff, simultaneous typing, `--continue`.
