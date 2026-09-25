# Safety and trust model

**Joining a vault means giving the other members shell-adjacent trust.** Read this once.

## What is shared

| Thing | Shared? | Notes |
|---|---|---|
| Conversation transcripts (`.vault/sessions/*.jsonl`) | **Yes** | Every turn, every tool call, every tool result |
| Subagent and workflow records | **Yes** | They live next to the transcript |
| Claude's project memory (`.vault/sessions/memory/`) | **Yes** | Deliberate: a vault is a shared workspace. Keep personal facts out. |
| `CLAUDE.md` at the vault root | **Yes** | Instructions Claude auto-loads, for every member |
| `.claude/settings.json`, `.claude/settings.local.json` at the vault root | **Yes** | Permission rules Claude applies, for every member |
| Your login / OAuth tokens | **No** | macOS Keychain / Windows Credential Manager, never in the folder |
| Your `~/.claude.json` (trust dialog answers, allowed tools, MCP enablement) | No | Per-person |
| `/rewind` checkpoints, background tasks, prompt history | No | Per-person |

## What actually leaks into a transcript

The verbatim output of every tool call. That includes:

- files Claude reads from **anywhere** on your Mac, not just the vault folder
- environment variables and secrets echoed in a terminal
- results from personal connectors (mail, calendar, finance) if you use them in the session
- your name and email as injected context

Rules of thumb: don't `cat` secrets in a vault session; keep personal connectors out of
vault sessions; use `vault private` when you want Claude in this folder without sharing.
After each run vault scans what you added for common key shapes and warns you to rotate;
the scan is a safety net, not a guarantee.

## Members can steer your Claude

`CLAUDE.md` and `.claude/settings*.json` in the vault sync to everyone. Any member can
change what Claude is told and what it may do without asking, **on your machine**.

Mitigations built in:

- `vault` warns whenever those files changed since your last run, and lists every
  pre-existing one the first time you join.
- vault refuses to launch if the shared settings set `defaultMode: bypassPermissions`,
  and refuses `--dangerously-skip-permissions` / `--permission-mode bypassPermissions`
  inside a vault. Use `vault private` for that.
- `vault join` shows those files *before* its first Claude call, and that self-test runs
  with `--setting-sources user --strict-mcp-config`, so shared hooks, env, apiKeyHelper and
  project MCP servers cannot execute on a newcomer's first call (observed on Claude Code
  2.1.280, 2026-09-24: a planted SessionStart hook fired with plain `claude -p` and did not
  with those flags). `--bare` is not used because it never reads OAuth/keychain logins.
  Normal `vault new`/`resume` launches are not isolated; that is what the drift warning is for.
- Handoff notes reach Claude as one quoted, descriptive sentence in the resume prompt,
  printed to you first, capped at 280 characters and refused if they look like a secret.
  Markers are as editable as transcripts: not authenticated.
- Never click "don't ask again" inside a vault. That grant is per-person, but it's the
  exact thing a poisoned `CLAUDE.md` would exploit.
- Only vault with people you'd trust to run commands near you.

## Transcripts are not authenticated

`.jsonl` files can be edited or planted by any member. Resuming one runs Claude on your
Mac with your credentials under context anyone in the share could have written.
STARTED-BY / LAST-BY in `vault sessions` are derived from transcript contents and are
labelled as a best guess, not an identity. A git-backed transport with signed commits is
the proper fix and is on the list for a later version.

## Leaving does not un-share

`vault leave` disconnects your Mac. Sessions you ran while joined remain in the vault,
on other members' Macs, and in OneDrive version history and recycle bin.

## Destructive operations

`claude project purge` on a joined folder removes only your local link, not the shared
store (verified). `rm -rf` of the linked project directory could traverse the link; don't.
The OneDrive recycle bin and version history are the recovery story.

## The lease is advisory

`vault` records who is in which session so teammates get a clear "in use by …" message.
It rides the same OneDrive sync as everything else, so it protects against *forgetting*,
not against a genuine race. If two people do type into one session at once, OneDrive keeps
both copies; vault quarantines the extra copy and `vault conflicts` lets you read what
was lost.
