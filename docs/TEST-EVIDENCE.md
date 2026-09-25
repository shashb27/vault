# Vault POC — Test Evidence

**Date:** 2026-07-09 · **Environment:** macOS, Claude Code 2.1.170, single machine
**Method:** two simulated users = two distinct directories (`u1/`, `u2/`) whose
`~/.claude/projects/<enc>` symlinks resolve to one shared `.vault` store (simulating
two synced OneDrive replicas with zero latency). Real two-machine OneDrive validation
is **T9 — still required before any demo** (see DESIGN.md §9).

| # | Check | Result | Evidence |
|---|-------|--------|----------|
| T1 | join: symlink + backup semantics | ✅ PASS | Pre-existing local session dir moved to `~/.claude/vault-backups/<enc>.<ts>/` (local-only, NOT shared, NOT merged); both users' symlinks healthy |
| T2 | U1 session lands in vault | ✅ PASS | `vault claude -p … --session-id c46be3c9…` → `c46be3c9….jsonl` in shared store; handoff marker written; lease released |
| T3 | R1/R3 visibility + attribution | ✅ PASS | `vault sessions` as U2: `c46be3c9… alice-sim alice-sim 2026-07-09 13:29 12264` (+ "heuristic, not verified identity" caveat printed) |
| T4 | R2 handoff — same session, bidirectional | ✅ PASS | U2 `vault resume c46be3c9… -p "what codeword?"` → **FALCON-42**. Same file grew 10→19 lines, **no new session file**, U2's turns grep-able in the original transcript (U1's view) |
| T5 | Lease block / steal / release | ✅ PASS | Foreign fresh lease → refusal with holder+time+override hint (exit 1); `--steal` warns and proceeds; lease file gone after run |
| T6 | Plain `claude` (no wrapper) | ✅ PASS | Bare `claude -p` at vault root → transcript in shared store (the whiteboard flow needs no wrapper after join) |
| T7 | leave restores + honest warning | ✅ PASS | Symlink removed; pre-join backup restored byte-identical; shared store untouched; "leave ≠ un-share" warning printed |
| T8 | Cross-user file work (dead paths) | ✅ PASS | U1's dir renamed away (all transcript paths dead); U2 `vault resume` + reorientation prompt → Claude re-resolved `notes.txt` at U2's root and appended correctly |
| T-torn | Truncated (mid-sync) transcript | ✅ PASS + finding | Wrapper refused ("ends mid-record… retry"). **Finding:** bare `claude --resume` TOLERATES a torn file — silently skips the bad tail and continues from stale context (answered from truncated history, exit 0). Silent context loss, not a crash → the wrapper gate is the only tell |
| T-purge | `claude project purge` on a joined project | ✅ PASS (good outcome) | Purge removes **only the local symlink**; shared store contents survive. Purge = broken join (re-join fixes), NOT team data loss |
| T9 | Two-machine OneDrive dry run | ⏳ **PENDING — requires a second person/machine** | Must verify: `.vault` dot-folder syncs, transcript+marker propagation time, dataless materialization, real resume |

## Bugs found & fixed during testing
1. `vault join` deduped registry entries by (user, host) only — a user joining from a
   second path was silently not registered. Fixed: dedupe by (user, host, vault_path).
2. `vault sessions` exited 1 when there were zero conflict copies (trailing
   `[[ … ]] && warn` as the function's last statement under `set -e`). Fixed with `if`.

## Empirical facts recorded for DESIGN.md
- `claude -p --resume <id>` appends to the same `<id>.jsonl` — no fork (T4).
- Bare resume on torn transcripts: tolerant, silent stale-context continuation (T-torn).
- `claude project purge` does not traverse the project-dir symlink (T-purge).
- Resume works when the recorded `cwd` no longer exists; with the reorientation
  system prompt, file work recovers on the new machine (T8).

## Simulation caveats (honest limits of this evidence)
- Zero sync latency: OneDrive propagation, dataless files, conflict copies and lease
  races are NOT exercised — that is exactly what T9 covers.
- Both simulated users share one macOS account: attribution labels were seeded into
  `users.json` manually; `leave` deregistration matched the real username instead of
  the sim labels (works correctly when users are actually distinct).

---

# Round 2 — Adversarial code review + fixes + regression (2026-07-10)

**Method:** Workflow with 3 finder agents (bash correctness / state safety / doc-contract
fidelity), every finding independently verified by a skeptic agent with sandboxed
repro attempts (24 agents total). **Result: 21 findings CONFIRMED, 0 refuted.** All fixed.

## Highlights
| Sev | Finding | Fix |
|---|---|---|
| **CRITICAL** | `encode_path` didn't match Claude's real encoding (ALL non-alphanumerics → `-`, per UTF-16 unit, >200-char truncation+hash). Any vault path with a space/underscore/etc. would **silently not share** — R1/R2 fail with zero errors. Round-1 tests passed only because sim paths had no specials. Verifier reproduced Claude's encoder from the binary and validated a python3 replica byte-for-byte (incl. unicode + hash suffix) | Exact encoder replica; realpath canonicalization; **empirical join self-test** (launch `claude -p` once, verify no new project dir appeared → symlink actually used; rollback + loud failure otherwise) |
| MAJOR | `finish_handoff` marked ANY transcript that changed mid-run as cleanly handed off — incl. other members' sessions syncing in → false-green handoff gate | Only mark sessions whose last-entry `cwd` is this user's vault root |
| MAJOR | Stale `.done` marker never invalidated on resume — resumer crash leaves false green | `vault resume` removes the marker at launch; rewritten only on clean exit |
| MAJOR | `vault sessions` never read `index.json` (bulk-download mitigation didn't exist) and rewrote the index per file | Index-first single-pass; only changed/new transcripts opened; one write. Regression shows `(0 transcript(s) opened, 2 served from index)` |
| MAJOR | `quarantine_conflicts` could overwrite an earlier quarantined copy — destroying the only copy of those turns | Collision-safe rename with timestamp suffix |
| MAJOR | `check_drift` wrote its baseline silently on first run — a new member is never warned about pre-existing CLAUDE.md/settings (the exact injection window) | First run now lists all pre-existing instruction/permission files with a review-now warning |
| MAJOR | `vault status <id>` exited 1 on healthy non-OneDrive vaults (`set -e` + trailing `&&`) | if-block |
| MINOR ×8 | Lease no-op for same user@host (+ premature delete by 2nd instance); greedy UUID capture in resume args; user `--append-system-prompt` clobbered; leave mid-session splits state; join message overstated subfolder sharing; memory/ conflict check missing; etc. | Live-pid lease check + pid-owned release; first-positional-only UUID; prompt merge; lease-guarded leave + `--force`; honest root-only message; stem-duplicate memory conflict check |

## Regression (fresh sim at hostile path `…/vault sim_2 (u1)`)
| Check | Result |
|---|---|
| R1 join at path with space/underscore/parens; self-test | ✅ symlink at claude's true encoding (`…-vault-sim-2--u1-`); "self-test: claude used the vault symlink ✓" |
| R2/R3 session + resume same-file + marker invalidation/rewrite | ✅ OSPREY-7 answered; marker cycle correct |
| R4 index caching | ✅ second listing: 0 opened, 2 from index |
| R5 status exit code (non-cloud) | ✅ exit 0 |
| R6 leave lease-guard + `--force` | ✅ refused, then forced; symlink removed |

`encode_path` unit checks: `/tmp/my vault_dir` → `-tmp-my-vault-dir` · `/tmp/café_vault`
→ `-tmp-caf--vault` (é = one UTF-16 unit = one dash, matching claude) · `/a/b.c-d` → `-a-b-c-d`.

---

# Round 3 — Opus QA fleet + orchestrator, end-to-end (2026-07-10)

**Method:** 3 parallel Opus QA agents in isolated sandboxes (hostile paths with spaces),
each running live `claude` sessions — QA1 functional E2E (10 checks), QA2 docs-vs-reality
audit (14 checks), QA3 negative/edge (13 checks) — then an Opus orchestrator (max effort)
reconciled all reports into a final verdict.

## Orchestrator verdict: **PASS_WITH_ISSUES**
| Area | Verdict |
|---|---|
| R1 shared visibility | ✅ verified (sim-level; cross-machine gated on T9) |
| R2 handoff, same session, bidirectional | ✅ verified (KESTREL-9 answered; zero forks; both users' turns in one file) |
| R3 isolated-but-visible | ✅ verified (+ inverse invariants: subfolder & `--private` sessions correctly stay private) |
| Lease machinery (block/steal/expired/release/leave-guard) | ✅ verified |
| Torn-transcript guard | ✅ verified (refuses before lease/launch; file untouched) |
| Conflict quarantine (incl. collision-safety) | ✅ verified |
| Drift warnings (first-run + changed) | ✅ verified |
| Join self-test | ⚠️ partially-verified → defect found + FIXED (below) |

## Defects confirmed by the fleet → resolution
| Sev | Defect | Resolution |
|---|---|---|
| MAJOR | Join self-test used a **global** `~/.claude/projects` before/after diff → unrelated concurrent claude activity caused a false "join FAILED" rollback (QA1 reproduced it live; fails closed, but the POC targets multi-session machines) | **FIXED:** self-test now nonce-scoped — finds the transcript containing a per-join nonce in the vault sessions dir (proof of traversal), checks stray dirs only for that nonce (still catches encoding divergence), never false-fails on concurrency. Re-verified with a deliberately injected concurrent project dir mid-join: join succeeded |
| MINOR | Every join left a throwaway self-test transcript in the shared pool forever | **FIXED:** transcript (+ handoff marker + session dir) deleted after verification; re-verified: sessions dir empty post-join |
| MINOR | DESIGN §4 described a phantom `title` column in `vault sessions` and omitted `size` | **FIXED** in DESIGN §4 |
| MINOR | Flag docs inconsistent (`vault help` omitted `leave --force`; README omitted `claude --steal`) | **FIXED** in help text + README + DESIGN |

## Orchestrator demo-readiness ruling
Safe to demo today (single-machine/same-account): the full CLI, R1/R2/R3, and every
guard — all exercised live at hostile paths. Still gated on **T9** (real two-machine
OneDrive): cross-machine propagation, dataless files, real conflict copies, lease races.
The orchestrator also correctly noted claude-binary facts (torn-tolerance of bare
resume, purge behavior, >200-char encoding) rest on Round-1/2 evidence, not re-verified
by this fleet.

---

# Round 4 — v0.2 → v0.3: named sessions, guided flow, Go port (2026-09-19)

**Method:** `tests/sim.sh` — two simulated users (alice, bob) whose `.vault` resolves to one
store (two OneDrive replicas with zero latency), at a hostile path with spaces and parens,
driving real `claude -p` calls (Claude Code 2.1.278, macOS). The same script runs against
either implementation via `VAULT_BIN`.

| Implementation | Checks | Result |
|---|---|---|
| `legacy/vault.sh` (bash, v0.2.1) | 65 | **65 pass, 0 fail** |
| `dist/vault` (Go, v0.3.0-beta.1, darwin/arm64) | 65 | **65 pass, 0 fail** |
| `go test ./...` (encoding incl. Windows paths, resolve, transcript parsing, leases, secrets, bypass flags) | 7 test funcs | pass |

Covered: init+join self-test · named `new` · duplicate-name refusal · resume by name /
case-insensitive prefix / id prefix, same file, bidirectional (LAST-BY updates) · rename,
old name gone · torn-transcript refusal · per-session lease block / `--steal` / release ·
marker-less non-tty refusal, own-session pass, idle-15-min pass · conflict quarantine +
`conflicts show` · `--json` · bare `vault` · `status <name>` · `doctor` exit codes ·
unnamed bare-claude session with first prompt, rename by id prefix · `vault claude`
compat · bypass-permission flags and settings refused · `show` · members memory ·
SIZE column and large warning · memory conflict copy · lease heartbeat · leave.

Read-only commands (`vault`, `status`, `doctor`, `members`, `conflicts`, `archive list`)
also run against the two live OneDrive vaults the team uses with the Go
binary: correct names/owners/states, real `fileproviderctl` state, exit codes as designed.

## Findings during this round
1. **Claude sometimes refused to repeat, to the second person, a "codeword" the first
   person gave it** once the transcript's cwd showed a different user. Fixed by telling
   Claude on resume that all members share the conversation by agreement and act as one
   principal; 4/4 subsequent manual runs and the suite pass. The suite's transferred fact
   is now phrased as team information rather than a secret, since the test is about
   context transfer, not about overriding Claude's own judgment.
2. **bash 3.2 (macOS default) cannot kill a sleeping child from a subshell trap**, so the
   first lease-heartbeat implementation left `sleep 300` processes holding the caller's
   pipe open. Rewritten with short sleep slices and stdio detached. Go uses a goroutine.
3. On macOS `/dev/null` is a character device, so Go's mode-bit "is a terminal" check was
   wrong; switched to `golang.org/x/term`.
4. macOS `wc -l` pads output; the test compared strings. Test bug, fixed.

## Not covered (honest limits)
- **Windows: nothing has run on a real Windows machine.** Cross-compiled binaries exist;
  the encoding of Windows paths, junction creation, OneDrive attributes and `attrib +P`
  are implemented from documentation and must be checked with `tests/windows-checklist.md`.
- Real OneDrive latency, dataless files and conflict races are exercised only by day-to-day
  use of the two live vaults, not by the suite.
- Two members on different Claude Code versions writing one transcript: untested.

## Release pipeline and install, end to end (2026-09-19, macOS)
- Tag push → GitHub Actions: `go vet`, `go test`, cross-compile 6 targets, pre-release with
  binaries, SHA256SUMS and both installers attached. Green for beta.1, beta.2, beta.3.
- Install one-liner `gh api …/contents/install.sh --raw | bash` in a **C locale** (bash 3.2):
  downloads the darwin/arm64 asset of the newest pre-release, installs to `~/.local/bin`.
- `vault update`: "already on" when current; `vault update <tag>` downgrades (binary
  replacement works); plain `vault update` upgrades beta.2 → beta.3. Installed binary's
  SHA-256 matches the release's SHA256SUMS.
- Bugs found and fixed on the way: `gh release download` without a tag ignores
  pre-releases (beta.1 installer/update failed); raw.githubusercontent.com returns 404 on
  a private repo (README now uses `gh api`); bash 3.2 read `$tag…` as one variable name
  in a C locale.

---

# 0.4.0 round — handoff notes + join hardening (2026-09-24)

Built from the design council's plan (journal #14a–#14z). Claude Code 2.1.280, macOS.

| Check | Result |
|---|---|
| `go vet ./... && go test ./...` (12 new tests: marker round-trip/legacy/garbage, parseNote, secrets, member matching, addressedToMe, Session JSON, index reads markers, interactive args, reorient sentence, self-test args, note flags, marker conflict sweep) | pass |
| `tests/sim.sh` against `dist/vault` 0.4.0-dev | **94 pass, 0 fail** (65 + J1–J3 + 26 handoff-note checks) |
| `VAULT_BIN=legacy/vault.sh tests/sim.sh` | **65 pass, 0 fail**; both new blocks skipped by the capability probe |
| Hook isolation, by hand: untrusted temp dir with a project `SessionStart` command hook | plain `claude -p` created the hook file; `claude -p --setting-sources user --strict-mcp-config` did not |
| Compatibility: released v0.3.2 binary reading a marker that carries `to`/`note` | lists the session as `handed off`, ignores the fields |
| Read-only smoke on the live Testing-vault with the 0.4 binary (`vault`, `sessions`, `sessions --json`, `status`, `doctor`, `members`, `help`) | all run; `ls -la` of handoff/, leases/, conflicts/ identical before and after; legacy marker rendered via the formatted path (`clean — by … 2026-09-03T06:43:09Z`); no `Waiting for you` (no `to` fields exist yet); `--json` carries `handoff_by`/`handoff_at` |

Not covered: the interactive exit prompt on Windows (W11 in tests/windows-checklist.md).
Open question the council raised for Shash: when did the 3-way fork in Testing-vault happen
relative to 0.2.0 (per-session leases + resume gate, 2026-09-19), and did both people launch
through vault? If it post-dates 0.2 with both through vault, the 0.5 merge should move up.

## Adversarial review of the 0.4 branch (2026-09-24/25)
Three reviewers (correctness, security, UX/docs), every finding sent to two independent
verifiers instructed to refute it with a reproduction. Final tally after the limit-cut
verifiers were re-run: 12 findings confirmed (2 duplicates), 3 refuted, 0 contested.
Confirmed and fixed:
1. `vault note` on a session with no clean-exit marker invented one attributed to the note
   author, permanently disarming the resume fork gate for sessions driven by plain `claude`
   (reproduced end-to-end by both verifiers). Now refuses; a note rides on a real marker only.
2. A resume that never touched the transcript (Claude failed to start, no turn) destroyed the
   note, because launch removes the marker up front. Now restored when nothing was added.
3. Resuming a session whose note was addressed to someone else silently dropped the note.
   Now carried forward at the resumer's exit and said so.
4. A bare `@login` that joined from two machines could never be addressed by prefix; and an
   exact `@Administrator` matched every Windows member. Prefix hits are now attributed to the
   owning login; an exact bare login on several machines is ambiguous (use host or login@host).
5. `@who` was stored uncapped and unchecked; marker fields were sanitised only on write.
   Now capped and secret-checked together, and cleaned on read.
6. The exit line printed `→ next: for sam: …` while the docs said `→ for sam: …`. Aligned.
Also confirmed and fixed (minor): "Waiting for you" could point at a session shown as
`in use`; `--clear` only as the second word and `VAULT_NO_NOTE` missing from help; control
characters in note text reached every member's terminal. Refuted: two Ctrl-C-at-the-prompt
variants (hardened anyway: SIGINT stays ignored until the marker and lease are written) and
"the join self-test still loads shared CLAUDE.md/auto-memory" (by design; only hooks, env,
apiKeyHelper and project MCP are isolated).
After fixes: `go test` green; `tests/sim.sh` **99 pass, 0 fail** (5 new checks: note never
invents a marker, untouched resume keeps the note, carry-forward, ambiguous login, host-specific).
