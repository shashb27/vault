# Vault — Shared Claude Sessions in a Shared Folder

**What is this?** Normally your Claude Code conversations belong to *you* — they're
stored in your home directory, and nobody else can see or continue them. A **Vault**
flips that: conversations belong to a **shared folder**. Anyone who opens Claude
inside the Vault sees the same pool of sessions, can **continue a conversation a
teammate started** (with all its context), and everyone's sessions stay visible to
the team.

```
  You  ──── opens claude in ────┐
                                ▼
                     📁 Shared OneDrive folder        ← conversations live HERE,
                        (the "Vault")                    not in anyone's home dir
                                ▲
  Teammate ─ opens claude in ───┘

  → your teammate can pick up your conversation exactly where you left off
```

Journal & history: [oxmiq/capsule#1559](https://github.com/oxmiq/capsule/issues/1559) · Design deep-dive: [DESIGN.md](DESIGN.md) · Test evidence: [TEST-EVIDENCE.md](TEST-EVIDENCE.md)

---

## Before you start (prerequisites)

You need, on **each** person's Mac:

1. **macOS** (this POC is macOS-only).
2. **Claude Code installed and logged in** — `claude --version` should print a version.
3. **python3** — already on every modern Mac (`python3 --version`).
4. **A shared OneDrive folder** both people can open in Finder — e.g. a folder in a
   shared SharePoint/OneDrive library. Both of you must see the *same* folder syncing
   on your own machines. (Your login/credentials are NOT shared — only conversations.)

## Install (once per person, ~30 seconds)

Put the `vault` command on your PATH. Either:

```bash
# option A: symlink (may need sudo)
sudo ln -sf "/Users/<you>/…/vault-poc/vault.sh" /usr/local/bin/vault

# option B: no sudo — add an alias to ~/.zshrc
echo 'alias vault="/Users/<you>/…/vault-poc/vault.sh"' >> ~/.zshrc && source ~/.zshrc
```

Replace the path with wherever this folder lives on YOUR machine. Test: `vault help`.

---

## Getting started — Person 1 (creates the vault)

```bash
cd ~/Library/CloudStorage/OneDrive-…/YourSharedFolder   # the shared OneDrive folder
vault init      # makes this folder a vault (creates a hidden .vault/ inside)
vault join      # connects YOUR claude to the vault (one-time)
```

`vault join` runs a quick self-test (it launches claude once, invisibly) and should end with:

```
vault: joined vault '…' as you@your-mac (self-test: claude used the vault symlink ✓; …)
```

Two one-time chores it will remind you about:
- **In Finder: right-click the `.vault` folder → "Always Keep on This Device."**
  (macOS gives no way to script this; press Cmd+Shift+. in Finder to see hidden folders.)
- Read the **trust notice** it prints — short version below in ⚠️.

Now just work:

```bash
vault claude            # opens claude in the vault — this session is SHARED
```

Talk to Claude normally. When you're done, **exit claude cleanly** (Ctrl+D or /exit) —
the wrapper then marks the session as "handed off" and waits for OneDrive to upload it.

> Plain `claude` (without the wrapper) also works inside the vault after joining —
> the wrapper just adds the safety rails (lease, handoff markers, health checks).

## Getting started — Person 2 (joins an existing vault)

```bash
cd ~/Library/CloudStorage/OneDrive-…/YourSharedFolder   # the SAME shared folder, on your Mac
vault join
```

Same deal: wait for the `✓`, pin `.vault` in Finder, read the trust notice.
The **first** time claude opens here it will ask you to trust the folder — that's
normal, it's per-person.

See what the team has been doing:

```bash
vault sessions
```

```
SESSION                                STARTED-BY*    LAST-TOUCH*    LAST-ACTIVE       SIZE
c46be3c9-…                             shash          shash          2026-07-10 09:29  15157
```

## The main event: continuing a teammate's conversation

1. Teammate finishes and exits claude (their wrapper writes a "clean handoff" marker).
2. You check the session is fully synced to your machine:
   ```bash
   vault status c46be3c9-…        # ← the session id from `vault sessions`
   ```
   Wait for three greens: **present: yes · tail: valid · handoff: clean**.
   If any is missing, OneDrive is still syncing — wait a minute and re-run.
3. Resume it:
   ```bash
   vault resume c46be3c9-…
   ```
   You're now in *their* conversation — same context, same session. Everything you
   say lands in the same shared transcript, so they'll see your turns too.

**Golden rule: one person in a session at a time.** The lease (below) reminds you,
but sync latency means it can't physically stop a race. Finish → exit → let it sync →
teammate resumes.

## Everyday commands

| Command | What it does |
|---|---|
| `vault claude` | Start working in the vault (shared session) |
| `vault claude --private` | A session that is **NOT** shared (runs from a local scratch dir) |
| `vault sessions` | List all shared sessions — who started, who last touched, when |
| `vault status` | Health check: joined? lease? sync state? conflicts? |
| `vault status <id>` | "Is this session safe to resume yet?" — the pre-resume gate |
| `vault resume <id>` | Continue a specific session (safely: checks sync, warns if in use) |
| `vault claude --steal` / `resume --steal` | Override someone's lease when you're SURE they're done |
| `vault leave` | Disconnect your claude from the vault (add `--force` if a lease blocks it) |
| `vault help` | Cheat sheet |

**If a command says the vault is "leased by <someone>":** they're (probably) mid-session.
Ask them, or if you're sure they're done: re-run with `--steal`.

---

## ⚠️ Read once before using (the honest part)

- **Everything Claude sees in a vault session is shared.** Every file it reads, every
  command output, every connector result (mail/calendar/…) lands in the shared
  transcript — even files from *outside* the vault. Don't `cat` secrets in a vault session.
- **Vault-mates get real influence over your Claude.** The vault syncs `CLAUDE.md`
  (instructions Claude auto-loads) and `.claude/settings.local.json` (permission
  grants). `vault status` warns when these change, but the rule is:
  **never click "don't ask again" inside a vault**, and only vault with people you'd
  trust to run commands on your machine.
- **Project memory is shared.** Claude's auto-memory for this folder is team memory —
  keep personal facts out.
- **Leaving doesn't un-share.** What you shared stays on teammates' machines and in
  OneDrive version history.
- **The conversation is shared; the workbench is not.** `/rewind` checkpoints,
  background tasks, and your up-arrow prompt history do NOT transfer between people.
- Your **login/credentials are never shared** (they live in your macOS Keychain).

## Troubleshooting

| Symptom | Cause & fix |
|---|---|
| `join` says **self-test inconclusive** | claude couldn't run (not logged in / offline). You're linked but unproven — run `vault claude`, say hi, exit, and check `vault sessions` shows it |
| `join` says **verification FAILED** | claude's internals changed and sessions would NOT share. Nothing was joined. File an issue on capsule#1559 |
| Teammate's session doesn't appear in `vault sessions` | OneDrive still syncing — check the OneDrive menu-bar icon; `vault status` shows what has arrived. Did they exit claude cleanly? |
| `resume` refuses: **"ends mid-record"** | The transcript is still mid-sync. Wait ~1 min, re-run `vault status <id>` until tail: valid |
| `resume` warns: **no clean-handoff marker** | The other person may still be in the session (or exited without the wrapper). Check with them before proceeding |
| **"leased by …"** but they say they're done | Their exit didn't sync yet, or they crashed. `--steal` |
| Claude asks for folder trust / permissions you thought were granted | Trust + permission grants are per-person, not shared via the vault (by design). Grant them yourself — but never "don't ask again" |
| Started claude in a **subfolder** — session missing | Sessions only share from the vault **root**. Always launch at the top of the vault folder (`vault claude` does this for you) |
| Session got **forked** into `…-MacBook.jsonl` | Two people hit one session at once. The wrapper quarantines the copy into `.vault/conflicts/` — the forked turns are only there; reconcile by hand |

## First time on two real machines?

That run doubles as **T9**, the one test still outstanding (everything else passed 3
verification rounds — see [TEST-EVIDENCE.md](TEST-EVIDENCE.md)). Follow "Getting
started" above in order, and afterwards note in
[capsule#1559](https://github.com/oxmiq/capsule/issues/1559): did `.vault` sync, how
long did a session take to appear on machine 2, and did `vault resume` answer from the
shared context. That closes the POC.

## Known limitations (v1)

Launch from the vault **root** only. One person per session at a time (sequential
handoff). macOS only. Big transcripts sync slower. The native `/resume` picker inside
claude shows no ownership info — use `vault sessions`. Full list: [DESIGN.md](DESIGN.md) §8.
