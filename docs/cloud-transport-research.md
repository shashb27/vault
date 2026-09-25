# Could vault use Claude's cloud instead of OneDrive?

_Research brief produced on 2026-09-24 by three docs-sourced research agents and a synthesizer. Nothing here was tested hands-on; every claim cites a docs page or a repo file. The underlying sweeps are on the journal issue (oxmiq/capsule#1559)._

**Cloud transport for vault — research brief**
Date: 2026-09-24 · For: Shash Bhaskar · Basis: three doc sweeps (code.claude.com, platform.claude.com, anthropic.com) plus `docs/remote-control-evaluation.md` and `docs/ARCHITECTURE.md` in `/Users/shashvath.bhaskar/code/vault`. Nothing here was tested hands-on.

## 1. Short answer

**Partly, and not with the cloud you are picturing.** The thing that carries *your* session between browser, phone and machines (Remote Control, cloud sessions, teleport, Projects) is still one-claude.ai-account-scoped in today's docs, so it cannot carry a session from you to a teammate; the 2026-09-19 conclusion in `docs/remote-control-evaluation.md` ("Every native multi-device feature is scoped to one claude.ai account. Team sharing of cloud sessions is view-only") still holds. What *is* documented as server-side, multi-client conversation state lives on the API side: Managed Agents sessions and the Agent SDK `SessionStore` adapter, both API-key/Commercial-Terms products, not the Team subscription, and neither is the Claude Code TUI. So OneDrive can be replaced today, but only by changing what "a vault session" runs on, which is a product decision rather than a transport swap.

## 2. What exists today

| Primitive | Cross-person? | Programmatic? | Where transcript lives | Needs |
|---|---|---|---|---|
| **Vault (OneDrive)** | yes, sequential | vault CLI | SharePoint/OneDrive `.jsonl` | OneDrive; per-user symlink/junction (`ARCHITECTURE.md`) |
| **Remote Control** | no: "grants no one else access" | no | Anthropic servers "while Remote Control is connected"; execution local | Pro/Max/Team/Enterprise; "API keys are not supported"; Team/Enterprise Owner toggle; not for ZDR orgs |
| **Cloud sessions** (claude.ai/code, `--cloud`) | view-only: "Recipients see the latest state … doesn't update in real time" | `claude -p "msg" --cloud <id>` for your own sessions only; interactive attach "not enabled for your account" | Anthropic | Pro/Max/Team, Enterprise premium seats; `allow_remote_sessions` policy |
| **Teleport** | no: "must be authenticated to the same claude.ai account" | CLI | private copy in your terminal | same account |
| **Desktop → cloud handoff** | no | no | new cloud session gets "a summary of the conversation", not the raw transcript | Desktop app |
| **Claude Code Projects** | no: "A project belongs to one user. You can't share a project" | no | Anthropic | Pro/Max |
| **Background sessions** | no | local `attach`/`logs` | `~/.claude/jobs`, daemon | local |
| **`claude --resume <transcript-path>`** | any file you can read | CLI flag | wherever the `.jsonl` is | documented in sessions.md; write-back path **could not confirm** |
| **Agent SDK `SessionStore`** | yes, via a store you own: "a session created on one host can be resumed on another host" | yes (`append`/`load` + optional list/delete) | your S3/Redis/Postgres (reference adapters + conformance suite) | API key ("Use the API key authentication methods"); SDK, not the CLI (inferred: docs list only SDK functions) |
| **Managed Agents session** | yes within a Console workspace: `ant beta:sessions connect` "attaches your terminal to an existing … session", loads transcript, follows live, can send/interrupt/approve | full REST/SDK; "Event history is persisted server-side and can be fetched in full" | Anthropic: "store conversation history, sandbox state, and outputs server-side" | API key + `managed-agents-2026-04-01` beta header; "enabled by default for all API accounts"; not ZDR/BAA eligible |

One correction to sweep 1: it states Managed Agents lets "any API caller with a session ID" continue a session "without requiring same-account authentication". The cited quote does not say that; the sessions-connect page says "a session in your workspace". Treat cross-workspace access as **could not confirm** and same-workspace access as documented.

## 3. Context, memory and tools under a cloud transport

**Context (the conversation).** Today it is the `.jsonl` file, whose format "is internal to Claude Code and changes between versions". Under Managed Agents the carrier is the session's event log, with a documented reconnect recipe ("Open a new stream. List the full event history … Tail the live stream"). Under `SessionStore` it is the mirrored entries keyed by projectKey (encoded cwd) + sessionId + subpath, with the caveat that mirror writes are best-effort: on failure the SDK "emits a `mirror_error` message … drops the batch, and continues", so adapters must dedupe by `entry.uuid`. Either way, vault's torn-file and conflict-copy problems become server truth or a store you control.

**Memory.** Today vault shares `MEMORY.md` on purpose (`DESIGN.md`: "concurrent memory writes are a OneDrive conflict-copy risk"). Managed Agents replaces this with workspace-scoped memory stores "mounted as a directory inside the session's sandbox", where "Every change … creates an immutable memory version" and updates can carry a `content_sha256` precondition. That is strictly better than today's shared file. Under `SessionStore`, memory is explicitly *not* carried: "`SessionStore` mirrors transcripts, not `CLAUDE.md` memory files … Mount a shared volume or sync those separately", so OneDrive (or git) would stay for memory. How `CLAUDE.md` is delivered into a Managed Agents session (mounted file vs. system prompt) **could not confirm**.

**Tools.** Today tools run on each person's Mac/Windows box against their own files. Managed Agents runs them in Anthropic's sandbox (Ubuntu 24.04, "Up to 8 GB" memory, "Up to 10 GB" disk) or a self-hosted sandbox that needs "A Linux host with `/bin/bash`", so Windows cannot host one and the person's local checkout is not the working tree. `SessionStore` runs tools on whichever SDK host resumes, "from a working directory matching the original run's". Credentials under Managed Agents move to Anthropic-managed Vaults ("Pass `vault_ids` at session creation"); how per-user MCP credentials would map **could not confirm**.

**Identity and attribution.** Vault's transcripts are unauthenticated (`safety.md`: "`.jsonl` files can be edited or planted by any member"). Managed Agents and SDK give a workspace-level identity (docs recommend a service account "so the workload has its own identity"), but no fetched page shows per-event user attribution or a session title field, so vault would still own names, member list, handoff notes and who-did-what. A shared claude.ai login as a service identity is off the table: "You may not share your Account login information … or Account credentials".

**What vault would have to change (inferred from the above).** Replace `lease.go` with session `status` (a `running` session "cannot be archived") or a CAS lease in the store; replace `transcript.go` scanning with event-history or store reads; keep `index.go`/`handoff.go` semantics but back them with a vault-owned metadata record; add an API-key/billing path ("$0.08 per session-hour" for `running` time plus tokens); and accept the loss of the Claude Code TUI, `/rewind` (file checkpointing "conflict[s] with the mirror"), hooks and local files.

## 4. Architecture options, ranked

1. **Hybrid: keep OneDrive as transport, add documented primitives around it.** Verdict: buildable now, smallest change, no product shift. Document Remote Control as the sanctioned way for the lease-holder to continue *their own* turn from phone/browser; evaluate `claude --resume <transcript-path>` to drop the symlink/junction and the "unverified on Windows" encoding in `ARCHITECTURE.md`; proceed with 0.5 conflict merge and 0.6 journal. Effort: small (inferred).
2. **Managed Agents as the session substrate.** Verdict: the only documented server-side, multi-person session today, but it is a different product from Claude Code. One Agent + one Environment + one Session per vault session + shared memory store; teammates resume with `ant beta:sessions connect <id>`. Effort: medium-large (inferred), plus API billing, beta status and no ZDR/BAA.
3. **Agent SDK + `SessionStore`.** Verdict: Anthropic's own documented design for "resume sessions across machines" and it keeps the Claude Code harness, but not the interactive CLI; vault must build the UX and keep memory on a separate sync. Effort: medium (inferred); the store is small, the UX is the cost.
4. **claude.ai cloud sessions / Remote Control as transport.** Verdict: not buildable for cross-person today. Every primitive is same-account; Team sharing is read-only; Projects are single-user. Effort: n/a, blocked on Anthropic.

## 5. Not documented / would need Anthropic

- **Would require Anthropic to ship:** account-to-account session transfer, editable Team sharing of cloud sessions, or attach-to-a-teammate's-session (already the revisit trigger in `remote-control-evaluation.md`).
- **Could not confirm:** whether `--resume <transcript-path>` appends new turns to that path; whether the CLI (not SDK) can use a `SessionStore`; whether every API key in a workspace can list/attach Managed Agents sessions or only the creating key; per-event attribution and session naming in Managed Agents; any `/rewind` equivalent there; sandbox idle timeout, session expiry and rate limits; cloud-session transcript retention beyond the general 30-day commercial figure; what gates "Attaching to an existing cloud session is not enabled for your account"; whether Team/Enterprise seats are contractually per-named-user (only the consumer account-sharing ban was found); whether auto-memory leaves the machine during Remote Control; `CLAUDE.md` mounting in Managed Agents; Agent SDK Windows/OneDrive behaviour.
- **All effort figures above are judgement, not documented.**

## 6. Recommendation for the next 1-2 quarters

Do not plan on Anthropic's session cloud as a cross-person transport this quarter; keep the revisit trigger as written. Ship the hybrid (option 1) now and keep 0.5/0.6 on the roadmap, since OneDrive stays the transport. Independently of transport, look at Team/Enterprise server-managed settings, which "let organization Owners centrally configure Claude Code", as a stronger version of vault's client-side bypass-permissions lockdown and drift warnings.

**Smallest experiment for the biggest unknown.** The biggest unknown is whether a teammate can actually pick up a Managed Agents session started by someone else's key in the same workspace, with usable context. The test: on a Console workspace with a service-account key, create one Agent and one Session with `initial_events`, send two turns from key A, then from a second machine run `ant beta:sessions connect <id>` with key B (a different personal key in the same workspace) and send a codeword turn; then fetch the full event history and check whether anything records which key sent which event. Half a day, a few dollars, and it settles workspace-level visibility, attribution, and whether option 2 is real. A second, cheaper check for option 1: run `claude --resume /abs/path/x.jsonl`, send one turn, and see which file grew. Record both results in a new `docs/cloud-transport-options.md`.

## 7. Sources

- `/Users/shashvath.bhaskar/code/vault/docs/remote-control-evaluation.md`, `docs/ARCHITECTURE.md`, `docs/DESIGN.md`, `docs/safety.md`, `CHANGELOG.md`
- https://code.claude.com/docs/en/remote-control.md
- https://code.claude.com/docs/en/claude-code-on-the-web.md
- https://code.claude.com/docs/en/desktop.md
- https://code.claude.com/docs/en/claude-projects.md
- https://code.claude.com/docs/en/sessions.md
- https://code.claude.com/docs/en/agent-view.md
- https://code.claude.com/docs/en/memory.md
- https://code.claude.com/docs/en/self-hosted-environments.md
- https://code.claude.com/docs/en/server-managed-settings.md
- https://code.claude.com/docs/en/data-usage.md
- https://code.claude.com/docs/en/legal-and-compliance.md
- https://code.claude.com/docs/en/agent-sdk/overview.md
- https://code.claude.com/docs/en/agent-sdk/sessions.md
- https://code.claude.com/docs/en/agent-sdk/session-storage.md
- https://code.claude.com/docs/en/agent-sdk/hosting.md
- https://platform.claude.com/docs/en/managed-agents/overview.md
- https://platform.claude.com/docs/en/managed-agents/sessions.md
- https://platform.claude.com/docs/en/managed-agents/session-operations.md
- https://platform.claude.com/docs/en/managed-agents/events-and-streaming.md
- https://platform.claude.com/docs/en/managed-agents/memory.md
- https://platform.claude.com/docs/en/managed-agents/environments
- https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes.md
- https://platform.claude.com/docs/en/managed-agents/cloud-containers.md
- https://platform.claude.com/docs/en/cli-sdks-libraries/cli/sessions-connect.md
- https://platform.claude.com/docs/en/about-claude/pricing.md
- https://platform.claude.com/docs/en/manage-claude/authentication.md
- https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-runner.md
- https://www.anthropic.com/legal/consumer-terms
- https://www.anthropic.com/legal/commercial-terms
