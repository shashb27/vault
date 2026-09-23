# Troubleshooting

Start with `vault doctor`. It checks Claude, python3, OneDrive, your join, pinning and
conflicts, and prints a fix for each ✗. Then look here.

## "You are not inside a vault folder"

You ran a vault command outside a vault. `vault` (no arguments) lists the vaults you've
joined on this Mac so you can `cd` into one. Sessions only share from the vault **root**;
launching in a subfolder makes a private session.

## "You have not joined this vault yet"

The folder is a vault (someone ran `vault init`) but your Mac isn't linked to it. Run
`vault join`.

## join says "could not verify"

Claude couldn't run (not logged in, or offline). You're joined but unproven: run
`vault new test`, say hi, exit, and confirm it appears in `vault sessions`.

## join says "join FAILED"

This Claude version stores sessions somewhere the vault doesn't expect. Nothing was
joined. Please [open an issue](https://github.com/shashb27/vault/issues) with your
`claude --version`.

## A teammate's session doesn't appear

OneDrive hasn't delivered it yet. Check the OneDrive menu-bar icon; `vault status` shows
whether OneDrive is running or paused. Did they exit Claude? Was their session started at
the vault root (not a subfolder)?

## `vault resume` waits, then says the transcript is incomplete

The file on your Mac ends mid-record; OneDrive is still delivering it. Wait a minute and
retry. `vault status <name>` shows the sync state. Bare `claude --resume` would silently
continue from the stale part, which is why the wrapper refuses.

## `vault resume` asks "continue anyway?"

The transcript is complete but nobody wrote a clean-exit marker for it. Either the last
person is still in it, their exit hasn't synced, or the session was started with plain
`claude` (no wrapper). Ask them, or wait. If it's been idle over 15 minutes and nobody
holds it, vault stops asking.

## "… is in this session right now"

Someone holds the session lease. Ask them to exit. If they've crashed or forgotten, and
you're sure: `vault resume <name> --steal`.

## Sessions show `(unnamed)`

They were started with plain `claude`, or with an older vault version. Name them:
`vault rename <id-prefix> <name>`. Use `vault new <name>` going forward.

## "two people were in one session at the same time" / conflicts

OneDrive kept two copies of a session. The extra copy was moved to `.vault/conflicts/`
and its turns are **not** in the main session. `vault conflicts` lists them and
`vault conflicts show <#>` prints the human/assistant turns so you can paste anything
important back into a live session. Prevention: one person per session; exit when done.

## "shared instruction/permission files changed"

A teammate edited `CLAUDE.md` or `.claude/settings*.json` in the vault. Those apply to
Claude on your Mac. Skim them before working. See [safety.md](safety.md).

## Claude asks for folder trust or permissions you already granted elsewhere

Trust and permission grants are per-person, by design. Grant them yourself. Never
"don't ask again" inside a vault.

## "refusing to start: … sets defaultMode to bypassPermissions"

Someone put `"defaultMode": "bypassPermissions"` in the vault's shared `.claude/settings`.
In a shared folder that would let any member make Claude run anything on your machine
without asking. Remove the line, and ask who added it. The same reason vault refuses
`--dangerously-skip-permissions`; use `vault private` if you need that for yourself.

## "what was just shared looks like it contains a secret"

The bytes you added to the transcript match a common key shape (AWS, Anthropic, GitHub,
Slack, Google, private key). Everyone in the vault can read it; rotate it if it's real.
The scan is pattern-based and warn-only.

## Windows: `vault join` fails to create the link

vault uses a directory junction (`mklink /J`), which needs no admin rights. If it fails,
run the command it prints by hand and send the error in an issue. Do not use WSL for the
vault: OneDrive lives in the Windows filesystem.

## Windows: sessions from this machine don't appear for teammates

Run `vault encode` in the vault folder and compare with the newest folder under
`%USERPROFILE%\.claude\projects` after a plain `claude -p "hi"` there. They must match
exactly. If they don't, open an issue with both strings; see `tests/windows-checklist.md`.

## `vault doctor` says "unknown command" but `vault help` works

A different program called `vault` is answering: usually **HashiCorp Vault** installed
with Homebrew, or an **old POC alias or shell function** (`alias vault=…` or `vault() { … }`)
in `~/.zshrc`. Run `type -a vault; vault version`; a function shows as "shell function from …". Ours prints `vault 0.3.x (…)`. Then either delete the
alias, put `~/.local/bin` first in PATH, or keep both by using a second name:
`ln -s ~/.local/bin/vault ~/.local/bin/cvault` and run `cvault`.

## `vault` isn't found after install

Open a new terminal. If it still isn't found, check that `~/.local/bin` is on your PATH
(`echo $PATH`) and that `~/.local/bin/vault` exists. Re-run the install one-liner from the README.

## An old `alias vault=…` in `~/.zshrc`

The installer removes it (backup in `~/.zshrc.vault-backup`). If you added one by hand
elsewhere, delete it; it shadows the new command.

## `vault update` fails to download

It fetches the release over plain HTTPS from github.com. If your network blocks that,
or you are on a private fork, install the GitHub CLI (https://cli.github.com), run
`gh auth login`, and `vault update` uses it as a fallback.

## My `.vault` folder keeps going "cloud-only"

macOS: pin it in Finder → right-click `.vault` → **Always Keep on This Device**; macOS has
no scriptable way to do this. Windows: `vault join` pins it with `attrib +P`; if it came
unpinned, run `attrib +P .vault /S /D` in the vault folder. `vault doctor` reports whether
it's pinned.

## Something else

`vault status` and `vault sessions --json` give the raw picture. Open an issue with their
output.
