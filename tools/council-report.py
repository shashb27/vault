#!/usr/bin/env python3
"""Render a vault design-council workflow journal (journal.jsonl) as one HTML page.

usage: council-report.py <journal.jsonl> <out.html> [title]
Shows every proposal, every debate round (critiques, revisions, votes), every
coordinator ruling, and the final build plan — verbatim from the agents.
"""
import html, json, sys

journal, out = sys.argv[1], sys.argv[2]
title = sys.argv[3] if len(sys.argv) > 3 else "Vault Design Council"

labels, results, order = {}, {}, []
for ln in open(journal):
    try:
        d = json.loads(ln)
    except Exception:
        continue
    if d.get("type") == "started" and d.get("agentId"):
        labels[d["agentId"]] = (d.get("label", ""), d.get("phase", ""))
        order.append(d["agentId"])
    elif "result" in d and d.get("agentId"):
        results[d["agentId"]] = d["result"]

def esc(s): return html.escape(str(s) if s is not None else "")
def para(s):
    return "".join(f"<p>{esc(p)}</p>" for p in str(s or "").split("\n\n") if p.strip()) or "<p class=muted>—</p>"
def ul(items):
    return "<ul>" + "".join(f"<li>{esc(i)}</li>" for i in (items or [])) + "</ul>" if items else "<p class=muted>none</p>"
def code_ul(items):
    return "<ul class=mono>" + "".join(f"<li><code>{esc(i)}</code></li>" for i in (items or [])) + "</ul>" if items else "<p class=muted>none</p>"

NAMES = {"A": "Architect A · adoption & friction", "B": "Architect B · reliability & sync", "C": "Architect C · trust & team scale"}

def proposal_block(p, heading_tag="h3"):
    pid = p.get("id", "?")
    return f"""
<article class="proposal lens-{esc(pid)}">
  <{heading_tag}><span class="chip">{esc(pid)}</span> {esc(p.get('title'))}</{heading_tag}>
  <p class="lede">{esc(p.get('one_liner'))}</p>
  <dl>
    <dt>Problem and evidence</dt><dd>{para(p.get('problem_evidence'))}</dd>
    <dt>Design</dt><dd>{para(p.get('design'))}</dd>
    <dt>Commands</dt><dd>{code_ul(p.get('user_facing_commands'))}</dd>
    <dt>Files touched</dt><dd>{code_ul(p.get('files_touched'))}</dd>
    <dt>Effort</dt><dd>{esc(p.get('effort_days'))} days</dd>
    <dt>Risks</dt><dd>{ul(p.get('risks'))}</dd>
    <dt>Verification</dt><dd>{para(p.get('verification'))}</dd>
    <dt>Why this first</dt><dd>{para(p.get('why_this_first'))}</dd>
  </dl>
</article>"""

# ---- collect
proposals, rounds, judges, plan = [], {}, {}, None
running = []
for aid in order:
    label, phase = labels[aid]
    r = results.get(aid)
    if r is None:
        running.append(label); continue
    if label.startswith("architect:"):
        proposals.append(r)
    elif label.startswith("round"):
        n = int(label[5:label.index(":")])
        rounds.setdefault(n, []).append(r)
    elif label.startswith("coordinator:round"):
        judges[int(label.rsplit("round", 1)[1])] = r
    elif label == "coordinator:plan":
        plan = r

parts = []
parts.append(f"""<title>{esc(title)}</title>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght@9..144,500;9..144,700&family=Source+Sans+3:wght@400;600&family=JetBrains+Mono:wght@400&display=swap">
<style>
:root{{--paper:#FAFAF7;--ink:#1B1F24;--muted:#6B7280;--line:#E2E0D8;--accent:#0E7C86;--accent-ink:#FFFFFF;
--a:#B45309;--b:#0E7C86;--c:#6D28D9;--soft:#F1F0EA;--warn:#B45309;--good:#15803D;}}
@media (prefers-color-scheme: dark){{:root:not([data-theme="light"]){{--paper:#15181C;--ink:#E8E6E0;--muted:#9AA0A8;--line:#2A2E34;--accent:#3FB5BF;--accent-ink:#0B0D10;
--a:#F59E0B;--b:#3FB5BF;--c:#A78BFA;--soft:#1C2026;--warn:#F59E0B;--good:#4ADE80;}}}}
:root[data-theme="dark"]{{--paper:#15181C;--ink:#E8E6E0;--muted:#9AA0A8;--line:#2A2E34;--accent:#3FB5BF;--accent-ink:#0B0D10;
--a:#F59E0B;--b:#3FB5BF;--c:#A78BFA;--soft:#1C2026;--warn:#F59E0B;--good:#4ADE80;}}
body{{background:var(--paper);color:var(--ink);font:16px/1.55 "Source Sans 3",system-ui,sans-serif;padding-block:0 4rem;padding-inline:16px;margin:0}}
main{{max-width:76ch;margin:0 auto}}
h1,h2,h3{{font-family:Fraunces,Georgia,serif;text-wrap:balance;line-height:1.15}}
h1{{font-size:2.1rem;font-weight:700;margin:2.2rem 0 .3rem}}
h2{{font-size:1.5rem;margin:2.6rem 0 .8rem;padding-top:1.2rem;border-top:1px solid var(--line)}}
h3{{font-size:1.2rem;margin:1.6rem 0 .3rem}}
.eyebrow{{font-size:.75rem;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);font-weight:600}}
.muted{{color:var(--muted)}}
.lede{{font-size:1.05rem;margin:.2rem 0 .8rem}}
.chip{{display:inline-block;font:600 .8rem/1 "JetBrains Mono",monospace;padding:.35em .55em;border-radius:.3em;color:#fff;vertical-align:middle;margin-right:.3em}}
.lens-A .chip,.chip.A{{background:var(--a)}}.lens-B .chip,.chip.B{{background:var(--b)}}.lens-C .chip,.chip.C{{background:var(--c)}}
dl{{display:grid;grid-template-columns:9.5rem 1fr;gap:.5rem 1rem;margin:0}}
dt{{color:var(--muted);font-size:.85rem;font-weight:600;padding-top:.15rem}}dd{{margin:0}}
dd p{{margin:0 0 .6rem}}dd p:last-child{{margin-bottom:0}}
ul{{margin:0;padding-left:1.2rem}}li{{margin:.15rem 0}}
.mono li{{list-style:none;margin-left:-1.2rem}}code{{font:.88em "JetBrains Mono",monospace;background:var(--soft);padding:.1em .35em;border-radius:.25em}}
.proposal{{padding:1rem 1.2rem;border-left:4px solid var(--line);margin:1rem 0;background:var(--soft);border-radius:0 .4rem .4rem 0}}
.lens-A{{border-left-color:var(--a)}}.lens-B{{border-left-color:var(--b)}}.lens-C{{border-left-color:var(--c)}}
.tally{{display:grid;grid-template-columns:repeat(3,1fr);gap:.6rem;margin:.8rem 0}}
.vote{{padding:.7rem .9rem;border:1px solid var(--line);border-radius:.4rem}}
.vote b{{font-family:"JetBrains Mono",monospace;font-weight:400}}
.ruling{{border:1px solid var(--accent);border-radius:.4rem;padding:1rem 1.2rem;margin:1rem 0}}
.ruling .eyebrow{{color:var(--accent)}}
.status{{padding:.6rem .9rem;border-radius:.4rem;background:var(--soft);font-size:.95rem}}
.crit h4{{margin:.8rem 0 .2rem;font-size:.95rem}}
details{{margin:.6rem 0}}summary{{cursor:pointer;font-weight:600}}
.steps li{{margin:.5rem 0}}.steps .verify{{color:var(--muted);font-size:.9rem;display:block}}
.consensus{{color:var(--good);font-weight:600}}.noconsensus{{color:var(--warn);font-weight:600}}
@media (max-width:520px){{dl{{grid-template-columns:1fr}}dt{{padding-top:.6rem}}.tally{{grid-template-columns:1fr}}}}
</style>
<main>
<p class="eyebrow">vault · design council · full record</p>
<h1>{esc(title)}</h1>
<p class="muted">Three architects with different lenses each proposed one feature, then critiqued each other, revised and voted, round by round, until a coordinator found unanimity. Everything below is the agents' own text, unedited.</p>
""")

if running:
    parts.append(f'<p class="status">In progress: {esc(", ".join(running))}. This page is republished as results land.</p>')

parts.append("<h2>1. Proposals as first written</h2>")
for p in proposals:
    parts.append(f'<p class="eyebrow">{esc(NAMES.get(p.get("id"), p.get("id")))}</p>' + proposal_block(p))

for n in sorted(rounds):
    parts.append(f"<h2>2.{n} Debate round {n}</h2>")
    rs = rounds[n]
    parts.append('<div class="tally">')
    for r in rs:
        me = r["revised_proposal"].get("id", "?")
        parts.append(f'<div class="vote"><span class="chip {esc(me)}">{esc(me)}</span> votes for <b>{esc(r.get("vote_for_id"))}</b><br><span class="muted">could accept: {esc(", ".join(r.get("would_accept_ids") or []))}</span></div>')
    parts.append("</div>")
    for r in rs:
        me = r["revised_proposal"].get("id", "?")
        parts.append(f'<h3><span class="chip {esc(me)}">{esc(me)}</span> {esc(NAMES.get(me, me))}</h3>')
        parts.append('<div class="crit">')
        for c in r.get("critiques", []):
            parts.append(f'<h4>Critique of {esc(c.get("target_id"))}</h4>{ul(c.get("points"))}')
        parts.append(f'<h4>Vote: {esc(r.get("vote_for_id"))}</h4>{para(r.get("vote_reason"))}')
        rp = r["revised_proposal"]
        parts.append(f'<details><summary>Revised proposal after this round: {esc(rp.get("title"))} ({esc(rp.get("effort_days"))} days)</summary>{proposal_block(rp, "h4")}</details>')
        parts.append("</div>")
    j = judges.get(n)
    if j:
        cls = "consensus" if j.get("consensus") else "noconsensus"
        word = "Unanimous" if j.get("consensus") else "No consensus yet"
        parts.append(f"""<div class="ruling"><p class="eyebrow">Coordinator ruling, round {n}</p>
<p><span class="{cls}">{word}</span> · leading: <span class="chip {esc(j.get('picked_id'))}">{esc(j.get('picked_id'))}</span> · {esc(j.get('tally'))}</p>
<dl><dt>Open disagreements</dt><dd>{ul(j.get('open_disagreements'))}</dd>
<dt>To the architects</dt><dd>{para(j.get('message_to_proposers'))}</dd></dl></div>""")

if plan:
    parts.append(f"<h2>3. The build plan</h2><h3><span class=\"chip {esc(plan.get('picked_id'))}\">{esc(plan.get('picked_id'))}</span> {esc(plan.get('title'))}</h3>")
    parts.append(f"<p class=lede>{esc(plan.get('summary'))}</p>")
    parts.append(f"<dl><dt>Final design</dt><dd>{para(plan.get('design_final'))}</dd>")
    steps = "".join(f'<li><b>{esc(s.get("n"))}.</b> {esc(s.get("description"))} <code>{esc(", ".join(s.get("files") or []))}</code><span class=verify>verify: {esc(s.get("verify"))}</span></li>' for s in plan.get("steps", []))
    parts.append(f"<dt>Steps</dt><dd><ol class=steps>{steps}</ol></dd>")
    parts.append(f"<dt>sim.sh checks</dt><dd>{ul(plan.get('sim_checks_to_add'))}</dd><dt>Go tests</dt><dd>{ul(plan.get('go_tests_to_add'))}</dd>")
    parts.append(f"<dt>Docs</dt><dd>{ul(plan.get('docs_to_update'))}</dd><dt>Definition of done</dt><dd>{ul(plan.get('definition_of_done'))}</dd>")
    parts.append(f"<dt>Dissent recorded</dt><dd>{para(plan.get('dissent_recorded'))}</dd></dl>")

parts.append("</main>")
open(out, "w").write("\n".join(parts))
print(f"wrote {out}: {len(proposals)} proposals, rounds {sorted(rounds)}, plan={'yes' if plan else 'no'}, running={running}")
