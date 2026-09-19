# vault — shared Claude Code sessions in a shared folder

Normally a Claude Code conversation belongs to *you*: it lives in your home folder and
nobody else can see it or continue it. A **vault** flips that. Conversations belong to a
**shared OneDrive folder**. Anyone who joins the vault sees the same sessions, can pick
up a conversation a teammate started with all of its context, and can hand it back.

```
  you ──── vault new planning-review ────┐
                                         ▼
                        📁 shared OneDrive folder  ← the conversation lives here
                                         ▲
  teammate ── vault resume planning-review ┘      ← continues exactly where you stopped
```

Sessions have **names**. You hand off `planning-review`, not `98c2c061-24a5-…`.

> **Private beta.** This repo is private; ask Shash for access. macOS only.
> Journal and history: [oxmiq/capsule#1559](https://github.com/oxmiq/capsule/issues/1559).

---

## Install (2 minutes, once per Mac)

You need: a Mac, [Claude Code](https://code.claude.com) installed and logged in
(`claude --version` prints a version), and the GitHub CLI logged in (`gh auth status`).

```bash
gh repo clone shashb27/vault ~/.vault-cli && ~/.vault-cli/install.sh
```

Open a new terminal, then:

```bash
vault doctor
```

Every line should be a ✓ except "inside a vault folder", which you fix next.
Later, `vault update` pulls the newest version.

## Start using it (5 minutes)

**If your team already has a vault** (a teammate made one):

```bash
cd ~/Library/CloudStorage/OneDrive-…/TheSharedFolder     # the folder you both see in Finder
vault join
```

**If you are the first person**, pick a folder in a SharePoint/OneDrive library that
your teammates also sync, and:

```bash
cd ~/Library/CloudStorage/OneDrive-…/TheSharedFolder
vault init
```

`init` creates the vault and joins you. Both commands run a quick self-check (one silent
Claude call) and tell you the one manual step OneDrive needs: in Finder, right-click the
hidden `.vault` folder → **Always Keep on This Device** (press `Cmd+Shift+.` to show
hidden folders).

Then, at any time, just type:

```bash
vault
```

It tells you where you are, what sessions exist, who is in them, and what to do next.

## Which situation are you in?

Find yours, follow the arrows. Every box is a real command or something you'll see on screen.

### A. "I'm new here and want to set up"

```mermaid
flowchart TD
    A(["New Mac"]) --> B["gh repo clone shashb27/vault ~/.vault-cli<br/>~/.vault-cli/install.sh"]
    B --> C["open a new terminal<br/>vault doctor"]
    C --> D{"all ✓ except<br/>'inside a vault folder'?"}
    D -- no --> E["follow the fix printed<br/>under each ✗"] --> C
    D -- yes --> F["cd into the shared OneDrive folder<br/>your team uses (see it in Finder)"]
    F --> G{"Did a teammate<br/>already make it a vault?"}
    G -- "yes (vault says 'not joined')" --> H["vault join"]
    G -- "no (vault says 'not inside a vault')" --> I["vault init"]
    H --> J["Finder: right-click .vault →<br/>Always Keep on This Device"]
    I --> J
    J --> K(["type  vault  — it tells you what's next"])
```

### B. "I want to start working on something"

```mermaid
flowchart TD
    A(["in the vault folder"]) --> B["vault"]
    B --> C{"Is there already a session<br/>about this topic?"}
    C -- no --> D["vault new planning-review"]
    C -- yes --> E["vault resume planning-review"]
    C -- "I'm not sure" --> F["vault resume<br/>(pick from a numbered list)"]
    D --> G(["work in Claude as usual"])
    E --> G
    F --> G
    G --> H["exit Claude: Ctrl+D or /exit"]
    H --> I(["✓ handed off — vault prints the command<br/>your teammate runs next"])
```

### C. "A teammate handed me a session"

```mermaid
flowchart TD
    A(["teammate says: vault resume planning-review"]) --> B["vault"]
    B --> C{"STATE column says…"}
    C -- "handed off" --> D["vault resume planning-review"] --> Z(["you're in their conversation,<br/>full context"])
    C -- "in use by alex" --> E["Alex still has it open.<br/>Wait, or ask Alex to exit."]
    E --> F{"Alex says they're done<br/>but STATE won't change?"}
    F -- yes --> G["vault resume planning-review --steal"] --> Z
    F -- no --> B
    C -- "syncing" --> H["vault resume planning-review<br/>(it waits for OneDrive, up to 90 s)"]
    H --> I{"ready in time?"}
    I -- yes --> Z
    I -- "no: 'still incomplete'" --> J["wait a minute, run it again"] --> H
    C -- "unknown" --> K["vault resume planning-review"]
    K --> L{"asks 'continue anyway?'"}
    L -- "session idle over 15 min" --> Z
    L -- "they might still be in it" --> M["answer N, ask them"] --> B
    L -- "you're sure they're done" --> N["answer y"] --> Z
    C -- "session isn't listed at all" --> O["OneDrive hasn't delivered it yet.<br/>Check the menu-bar icon, wait, run vault again"] --> B
```

### D. "I'm done for now"

```mermaid
flowchart TD
    A(["finished, or handing over"]) --> B["exit Claude: Ctrl+D or /exit"]
    B --> C["vault writes the clean-exit marker<br/>and waits for the OneDrive upload"]
    C --> D(["✓ handed off — tell your teammate:<br/>vault resume planning-review"])
    B -. "forgot and just closed the terminal?" .-> E["your teammate will be asked 'continue anyway?'<br/>or see 'in use by you' for up to 60 min"]
    E --> F["run  vault resume planning-review  and exit cleanly,<br/>or tell them to use --steal"]
```

### E. "Something looks wrong"

```mermaid
flowchart TD
    A(["something's off"]) --> B["vault doctor"]
    B --> C{"what does it say?"}
    C -- "✗ not joined" --> D["vault join"]
    C -- "✗ OneDrive not running / paused" --> E["start OneDrive or resume sync<br/>from the menu-bar icon"]
    C -- "✗ not pinned" --> F["Finder: right-click .vault →<br/>Always Keep on This Device"]
    C -- "✗ conflict copies" --> G["vault conflicts"]
    G --> H["vault conflicts show 1<br/>read the lost turns, paste what matters<br/>into a live session"]
    C -- "! shared instruction files changed" --> I["skim CLAUDE.md / .claude/settings<br/>a teammate edited them — they steer Claude on YOUR Mac"]
    C -- "all ✓ but a session is missing" --> J["OneDrive hasn't delivered it.<br/>Did they exit Claude? Was it started at the vault root?"]
    C -- "all ✓, still stuck" --> K["vault status planning-review<br/>then open an issue with the output"]
```

### F. "I want Claude in this folder, but NOT shared"

```mermaid
flowchart LR
    A(["private work in a vault folder"]) --> B["vault private"] --> C(["normal Claude, nothing lands in the vault"])
```

## Everyday use

| You want to… | Run |
|---|---|
| Start a conversation the team can pick up | `vault new planning-review` |
| See what's there and who touched what | `vault` or `vault sessions` |
| Continue a teammate's conversation | `vault resume planning-review` |
| Continue one, choosing from a list | `vault resume` |
| Give a session a better name | `vault rename planning-review q4-plan` |
| Check the setup | `vault doctor` |
| Work in this folder **without** sharing | `vault private` |

Inside Claude everything is normal. When you're done, **exit Claude** (`Ctrl+D` or
`/exit`). That's the handoff: vault marks the session as finished, waits for OneDrive to
upload it, and prints the exact command your teammate runs next.

`vault resume` does the waiting for you: if the transcript is still syncing, or the
previous person hasn't exited yet, it waits and tells you why. It never silently continues
from a half-synced conversation.

**One rule: one person in a session at a time.** If two people type into the same session
at once, OneDrive keeps two copies and the second person's turns end up in a "conflict
copy" instead of the shared conversation. `vault` warns you when someone is in a session,
and `vault conflicts` shows you what was lost if it happens anyway.

## Read once: what "shared" means

- **Everything Claude reads or prints in a vault session is shared** with every member,
  including files from outside the vault, command output, and personal connectors
  (mail, calendar). Don't `cat` secrets in a vault session.
- **Members can steer each other's Claude.** `CLAUDE.md`, `.claude/settings*.json` and
  Claude's project memory in the vault folder are shared and editable by everyone, and
  they apply on *your* Mac. `vault` warns when they change. Never click "don't ask
  again" inside a vault, and only vault with people you'd trust to run commands on your
  machine.
- **Leaving doesn't un-share.** What you shared stays on teammates' Macs and in OneDrive
  version history.
- **The conversation is shared; the workbench is not.** `/rewind` checkpoints,
  background tasks, prompt history and your permission grants stay on your own Mac.
- Your **login is never shared**; it stays in your macOS Keychain.

Full trust model: [docs/safety.md](docs/safety.md).

## More

- [docs/handoff.md](docs/handoff.md) — the two-person handoff, step by step, with what you'll see (the long form of chart C)
- [docs/troubleshooting.md](docs/troubleshooting.md) — symptoms → causes → fixes
- [docs/safety.md](docs/safety.md) — trust model, what leaks, what doesn't
- [docs/DESIGN.md](docs/DESIGN.md) — how it works (symlinked project dir, leases, sync gates)
- [docs/TEST-EVIDENCE.md](docs/TEST-EVIDENCE.md) — verification rounds
- [CHANGELOG.md](CHANGELOG.md)

Bugs and ideas: [open an issue](https://github.com/shashb27/vault/issues).

## For developers

```bash
git clone https://github.com/shashb27/vault ~/code/vault
cd ~/code/vault && ./install.sh      # links ~/.local/bin/vault to this checkout
tests/sim.sh                         # two simulated users, real Claude calls, ~3 min
```

`vault.sh` is one bash file; all transcript parsing is in the embedded Python block near
the top (`vpy`). `.vault/` layout is unchanged from v0.1, so old and new clients can share
a vault.
