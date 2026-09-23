# Windows verification checklist (manual)

The Go binary cross-compiles for Windows, but nothing below has been run on a real
Windows machine yet. Go through this once on a Windows machine that has Claude Code
installed natively (not WSL) and OneDrive signed in. Record results in
`docs/TEST-EVIDENCE.md` under a "Windows" heading, including `claude --version`,
the Windows build, and the exact outputs where asked.

Prerequisites: `gh auth login` done, access to the repo, Claude Code logged in.

## W1. Install

```powershell
irm https://raw.githubusercontent.com/shashb27/vault/next/install.ps1 | iex
vault version
vault doctor
```

Expect: version prints with `windows/amd64`; doctor shows ✓ for Windows, Claude Code, gh.

## W2. Encoding matches Claude (the critical check)

Pick a folder under OneDrive, e.g. `C:\Users\<you>\OneDrive - <Your Org>\vault-win-test`.

```powershell
cd "C:\Users\<you>\OneDrive - <Your Org>\vault-win-test"
claude -p "reply OK"
dir $env:USERPROFILE\.claude\projects | sort LastWriteTime | select -last 3
vault encode
```

Expect: the newest folder under `.claude\projects` has EXACTLY the name `vault encode`
prints. If it differs, record both strings verbatim; that is the first thing to fix.
Also record whether the transcript's `cwd` field uses `C:\` or `c:\` and backslashes:

```powershell
Select-String -Path "$env:USERPROFILE\.claude\projects\<that folder>\*.jsonl" -Pattern '"cwd":"[^"]*"' | select -first 1
```

## W3. init / join with a junction

```powershell
vault init win-test
dir $env:USERPROFILE\.claude\projects | findstr /i "vault-win-test"
fsutil reparsepoint query "$env:USERPROFILE\.claude\projects\<encoded name>"
```

Expect: "joined … (verified: Claude writes into the vault)"; the projects entry is a
`<JUNCTION>` pointing at `...\vault-win-test\.vault\sessions`; the self-test transcript
was removed from `.vault\sessions`. If the join says "could not verify", note it.

## W4. Pinning

```powershell
attrib "$env:USERPROFILE\OneDrive - <Your Org>\vault-win-test\.vault"
vault status
```

Expect: attrib shows `P` (pinned) on `.vault`; status says "pinned". If `attrib +P` is
not accepted on this Windows build, record the error.

## W5. Named session, handoff to a Mac

```powershell
vault new win-falcon -p "For the whole team: our release train is named FALCON-42. Reply with just OK."
vault sessions
```

Then on the Mac, in the same OneDrive folder: `vault sessions` should show `win-falcon`
with STARTED-BY = your Windows username, STATE `handed off`. Then
`vault resume win-falcon -p "What is our release train named? Reply with just the name."`
should answer FALCON-42. Record how long the session took to appear on the Mac.

## W6. Mac → Windows handoff

On the Mac: `vault new mac-osprey -p "Team fact: the build server is OSPREY-7. Reply OK."`.
On Windows: `vault sessions` until it shows `handed off`, then
`vault resume mac-osprey -p "What is the build server called? Reply with just the name."`.
Expect OSPREY-7. Record propagation time.

## W7. Cloud-only file detection

In OneDrive settings, free up space on the `.vault\sessions` folder (or un-pin and wait),
then `vault status mac-osprey`. Expect "cloud-only" in the onedrive line and
`vault resume` to materialise it before continuing.

## W8. Terminal rendering

Run `vault` in Windows Terminal, in classic PowerShell, and in cmd.exe. Expect colours
in the first two and no stray `←[` escape codes in any.

## W9. Update

```powershell
vault update
```

Expect: downloads the newest release for windows/amd64 and replaces itself (old copy at
`vault.exe.old`). Run `vault version` afterwards.

## W10. Leave

```powershell
vault leave
dir $env:USERPROFILE\.claude\projects | findstr /i "vault-win-test"
```

Expect: junction removed; `vault` in that folder says "not joined".

## What to send back

The `.jsonl` `cwd` sample from W2, the outputs of `vault encode` and the projects listing,
`fsutil reparsepoint query` output, propagation times from W5/W6, and anything that
printed an error.
