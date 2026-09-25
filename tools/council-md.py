#!/usr/bin/env python3
"""Emit the council journal as markdown sections: proposals | round N | plan.
usage: council-md.py <journal.jsonl> <section> [outfile]   section = proposals | round<N> | plan"""
import json, sys
journal, section = sys.argv[1], sys.argv[2]
labels, results, order = {}, {}, []
for ln in open(journal):
    try: d = json.loads(ln)
    except Exception: continue
    if d.get("type") == "started" and d.get("agentId"):
        labels[d["agentId"]] = d.get("label", ""); order.append(d["agentId"])
    elif "result" in d and d.get("agentId"):
        results[d["agentId"]] = d["result"]
NAMES = {"A": "Architect A — adoption & friction lens", "B": "Architect B — reliability & sync lens", "C": "Architect C — trust & team-scale lens"}
def bl(items): return "\n".join(f"- {i}" for i in (items or [])) or "_none_"
def cl(items): return "\n".join(f"- `{i}`" for i in (items or [])) or "_none_"
def prop(p, h="###"):
    return f"""{h} {p.get('id')} · {p.get('title')}  *({p.get('effort_days')} days)*

**{p.get('one_liner')}**

**Problem and evidence**

{p.get('problem_evidence')}

**Design**

{p.get('design')}

**Commands**

{cl(p.get('user_facing_commands'))}

**Files touched**

{cl(p.get('files_touched'))}

**Risks**

{bl(p.get('risks'))}

**Verification**

{p.get('verification')}

**Why this first**

{p.get('why_this_first')}
"""
out = []
if section == "proposals":
    out.append("## 📓 Journal Entry #14a — Design council: the three proposals\n\nThree architect agents (same model as this session), each with a different lens, read the repo at v0.3.2 and proposed one flagship feature. Verbatim.\n")
    for aid in order:
        if labels[aid].startswith("architect:") and aid in results:
            p = results[aid]; out.append(f"## {NAMES.get(p.get('id'), p.get('id'))}\n"); out.append(prop(p))
elif section.startswith("round"):
    n = section[5:]
    rs = [(labels[a], results[a]) for a in order if labels[a].startswith(f"round{n}:") and a in results]
    j = next((results[a] for a in order if labels[a] == f"coordinator:round{n}" and a in results), None)
    out.append(f"## 📓 Journal Entry #14 — Design council: debate round {n}\n")
    out.append("| architect | votes for | could accept |\n|---|---|---|")
    for _, r in rs:
        out.append(f"| {r['revised_proposal']['id']} | **{r['vote_for_id']}** | {', '.join(r.get('would_accept_ids') or [])} |")
    out.append("")
    for _, r in rs:
        me = r['revised_proposal']['id']
        out.append(f"### {NAMES.get(me, me)}\n")
        for c in r.get("critiques", []):
            out.append(f"**Critique of {c.get('target_id')}**\n\n{bl(c.get('points'))}\n")
        out.append(f"**Vote: {r['vote_for_id']}** — {r['vote_reason']}\n")
        rp = r['revised_proposal']
        out.append(f"<details><summary>Revised proposal after this round: {rp.get('title')} ({rp.get('effort_days')} days)</summary>\n\n{prop(rp, '####')}\n</details>\n")
    if j:
        out.append(f"### Coordinator ruling, round {n}\n\n**{'Unanimous' if j.get('consensus') else 'No consensus yet'}** · leading: **{j.get('picked_id')}** · {j.get('tally')}\n\n**Open disagreements**\n\n{bl(j.get('open_disagreements'))}\n\n**To the architects**\n\n{j.get('message_to_proposers')}\n")
elif section == "plan":
    p = next((results[a] for a in order if labels[a] == "coordinator:plan" and a in results), None)
    if p:
        out.append(f"## 📓 Journal Entry #14z — Design council: the decision and build plan\n\n### Picked: {p['picked_id']} · {p['title']}\n\n{p['summary']}\n\n**Final design**\n\n{p['design_final']}\n\n**Steps**\n")
        for s in p.get("steps", []):
            out.append(f"{s['n']}. {s['description']}  \n   files: {', '.join('`'+f+'`' for f in s.get('files') or [])}  \n   verify: {s['verify']}")
        out.append(f"\n**sim.sh checks to add**\n\n{bl(p.get('sim_checks_to_add'))}\n\n**Go tests to add**\n\n{bl(p.get('go_tests_to_add'))}\n\n**Docs to update**\n\n{bl(p.get('docs_to_update'))}\n\n**Definition of done**\n\n{bl(p.get('definition_of_done'))}\n\n**Dissent recorded**\n\n{p.get('dissent_recorded')}\n")
text = "\n".join(out)
if len(sys.argv) > 3: open(sys.argv[3], "w").write(text)
print(len(text), "chars")
