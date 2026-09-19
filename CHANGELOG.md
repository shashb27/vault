# Changelog

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
