# Do Claude Code's native features replace the vault?

**Date:** 2026-09-19 · **Method:** documentation review of code.claude.com (no hands-on test) ·
**Question:** does any first-party Claude Code feature let one teammate continue a session
another teammate started, which is the vault's core job?

**Answer: no.** Every native multi-device feature is scoped to one claude.ai account. Team
sharing of cloud sessions is view-only. So the vault fills a gap rather than duplicating a
product feature. Details and sources below.

## Remote Control

Connects claude.ai/code or the Claude mobile app to a Claude Code session running on your
own machine, so you can continue from a phone or another browser. Conversation and subagent
progress stay in sync across your connected devices.

- Different person? **No.** "Auto-connect signs in with your own claude.ai account, so a
  session it starts appears only in your own account's Claude apps and grants no one else
  access."
- Requirements: Pro, Max, Team or Enterprise subscription; API keys not supported; on Team
  and Enterprise an Owner must enable the Remote Control toggle in admin settings.
- Where state lives: execution stays on your machine, but "while Remote Control is
  connected, the session transcript … is stored on Anthropic servers."
- Source: https://code.claude.com/docs/en/remote-control.md

## Teleport

Pulls a cloud session into your terminal: checks out the branch and loads the full
conversation history. The terminal gets its own copy; new work there does not flow back.

- Different person? **No.** "You must be authenticated to the same claude.ai account used
  in the cloud session."
- Requirements: claude.ai subscription auth; the org's `allow_remote_sessions` policy enabled.
- Source: https://code.claude.com/docs/en/claude-code-on-the-web.md

## Cloud sessions (claude.ai/code, `claude --cloud`)

Sessions can be made visible to your claude.ai organization (Team/Enterprise) or public.
"Recipients see the latest state when they open the link, but their view doesn't update in
real time." Nothing in the docs lets a recipient continue or steer the session.

- Different person? **View only.**
- Requirements: research preview for Pro, Max and Team, and Enterprise with premium or
  Chat + Claude Code seats.
- Source: https://code.claude.com/docs/en/claude-code-on-the-web.md

## Export / import

`/export` copies the conversation as readable text. There is no import. The docs warn the
transcript format "is internal to Claude Code and changes between versions."
Source: https://code.claude.com/docs/en/sessions.md

## Background sessions (`--bg`, `attach`, `logs`)

Single machine: state lives under `~/.claude/jobs/<id>` and `~/.claude/daemon/`.
Source: https://code.claude.com/docs/en/agent-view.md

## Windows

Native Windows is officially supported: "Windows 10 1809+ or Windows Server 2019+ … You can
run Claude Code natively on Windows or inside WSL." Transcripts live under
`~/.claude/projects/<project>/`, with `<project>` being the working directory path with
non-alphanumeric characters replaced by `-`. The docs give no Windows-specific example of
the encoding, so the vault's join self-test (launch once, confirm the transcript landed in
the vault) remains the guard.
Sources: https://code.claude.com/docs/en/setup.md, https://code.claude.com/docs/en/sessions.md

## Comparison

| | Cross-person handoff | Names | Team-wide list | Where transcript lives |
|---|---|---|---|---|
| **Vault (OneDrive)** | yes, sequential | yes | yes | your SharePoint/OneDrive |
| Remote Control | no (own account) | yes | own sessions | Anthropic servers while connected |
| Cloud sessions | view only | yes | org visibility | Anthropic |
| Teleport | no (same account) | inherited | own | copy to your terminal |
| Background sessions | no | yes | local | local |
| `/export` | text only, no import | n/a | n/a | wherever you save it |

## Recommendation

Keep the vault as the transport for cross-person handoff; nothing native does it. Remote
Control is complementary for one person moving between their own devices and can be used
inside a vault session like any other flag. Revisit this page if Anthropic ships
account-to-account session transfer or editable team sharing of cloud sessions; at that
point the vault's lasting value is naming, the team view, and the handoff etiquette, not
sync.

Not verified hands-on: none of the features above were exercised in this evaluation; all
statements are from the documentation as of the date above.
