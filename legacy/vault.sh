#!/usr/bin/env bash
# vault — shared Claude Code sessions in a shared folder.
#
# LEGACY: this is the v0.2.1 bash implementation, kept as the behavioural reference
# for the Go binary (cmd/vault). It is macOS-only and no longer installed by default.
#
# A vault is a folder (OneDrive for now) that owns Claude Code session state.
# After a one-time `vault join`, Claude opened at the vault root reads and
# writes a session pool that every member sees. Sessions have NAMES, so you
# hand off "planning-review", not a UUID.
#
# Repo & docs: https://github.com/shashb27/vault
# Trust model:  docs/safety.md — joining a vault gives members shell-adjacent
#               trust. Transcripts, memory, CLAUDE.md and .claude settings in
#               the vault are shared and unauthenticated.
set -euo pipefail

VAULT_VERSION="0.2.1"
VAULT_DIRNAME=".vault"
LEASE_TTL_MINUTES=60
UPLOAD_WAIT_SECS=45
GATE_WAIT_SECS=90            # how long `vault resume` waits for sync before asking
IDLE_OK_SECS=900             # marker-less session idle this long counts as finished
LEASE_HEARTBEAT_SECS="${VAULT_LEASE_HEARTBEAT_SECS:-300}"   # lease refresh while Claude runs
BIG_SESSION_BYTES="${VAULT_BIG_SESSION_BYTES:-5000000}"   # warn above this: slow to sync, heavy to resume
SELF_USER="${VAULT_TEST_USER:-$(whoami)}"   # override is for tests/sim.sh only
SELF_HOST="${VAULT_TEST_HOST:-$(hostname -s)}"
STATE_DIR="$HOME/.claude/vault-local-state"   # per-user, never synced
BACKUP_DIR="$HOME/.claude/vault-backups"      # per-user, never synced
REGISTRY="$STATE_DIR/vaults.list"             # vault roots this user has joined
INSTALL_DIR="$(cd "$(dirname "$(realpath "$0")")" && pwd)"

# ---------- output helpers ----------

if [[ -t 1 ]]; then
  B=$'\e[1m'; D=$'\e[2m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'
  export VAULT_COLOR=1
else
  B=''; D=''; G=''; Y=''; R=''; N=''
  export VAULT_COLOR=0
fi

die()  { echo "${R}✗${N} $*" >&2; exit 1; }
warn() { echo "${Y}!${N} $*" >&2; }
ok()   { echo "${G}✓${N} $*"; }
info() { echo "  $*"; }
hint() { echo "${D}  → next:${N} $*"; }

# ---------- embedded python library ----------
# All transcript parsing lives here (one place to fix when Claude's internal
# format changes). Bash keeps process control, leases and launching.

vpy() {
  VAULT_ROOT="${VAULT_ROOT:-}" VAULT_DIR="${VAULT_DIR:-}" SESSIONS_DIR="${SESSIONS_DIR:-}" \
  SELF_USER="$SELF_USER" SELF_HOST="$SELF_HOST" LEASE_TTL_MINUTES="$LEASE_TTL_MINUTES" BIG_SESSION_BYTES="$BIG_SESSION_BYTES" \
  python3 - "$@" <<'PY'
import json, sys, os, glob, re, time, datetime

ROOT = os.environ.get("VAULT_ROOT", "")
VDIR = os.environ.get("VAULT_DIR", "")
SDIR = os.environ.get("SESSIONS_DIR", "")
SELF_USER = os.environ.get("SELF_USER", "")
SELF_HOST = os.environ.get("SELF_HOST", "")
TTL = int(os.environ.get("LEASE_TTL_MINUTES", "60"))
COLOR = os.environ.get("VAULT_COLOR") == "1"
UUID_RE = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

def c(code, s):
    return f"\x1b[{code}m{s}\x1b[0m" if COLOR else s
def dim(s):  return c("2", s)
def bold(s): return c("1", s)
def grn(s):  return c("32", s)
def yel(s):  return c("33", s)

# ---- Claude Code project-dir encoding (verified against 2.1.170 and 2.1.278) ----
def encode_path(p):
    b = p.encode("utf-16-le")
    units = [int.from_bytes(b[i:i+2], "little") for i in range(0, len(b), 2)]
    enc = "".join(chr(u) if (48 <= u <= 57 or 65 <= u <= 90 or 97 <= u <= 122) else "-" for u in units)
    if len(enc) > 200:
        h = 0
        for u in units:
            h = (h * 31 + u) & 0xFFFFFFFF
        if h >= 1 << 31: h -= 1 << 32
        h = abs(h)
        d = "0123456789abcdefghijklmnopqrstuvwxyz"
        s = "0" if h == 0 else ""
        while h:
            s = d[h % 36] + s; h //= 36
        enc = enc[:200] + "-" + s
    return enc

def load_json(path, default):
    try:
        with open(path) as f: return json.load(f)
    except Exception:
        return default

def users():
    return load_json(os.path.join(VDIR, "users.json"), {"users": []}).get("users", [])

def user_for(cwd):
    if not cwd: return "?"
    for u in users():
        vp = u.get("vault_path")
        if vp and (cwd == vp or cwd.startswith(vp + "/")): return u["user"]
    if cwd.startswith("/Users/"):
        parts = cwd.split("/")
        if len(parts) > 2: return parts[2]
    return "?"

def text_of(msg):
    content = msg.get("content") if isinstance(msg, dict) else None
    if isinstance(content, str): return content
    if isinstance(content, list):
        for blk in content:
            if isinstance(blk, dict) and blk.get("type") == "text": return blk.get("text", "")
    return ""

def scan(path):
    """Full scan of one transcript. Called only when size/mtime changed."""
    first_cwd = last_cwd = None
    title = None
    first_prompt = None
    with open(path, "rb") as f:
        for raw in f:
            try: d = json.loads(raw)
            except Exception: continue
            if not isinstance(d, dict): continue
            if "cwd" in d:
                if first_cwd is None: first_cwd = d["cwd"]
                last_cwd = d["cwd"]
            t = d.get("type")
            if t == "custom-title" and d.get("customTitle"):
                title = d["customTitle"]
            elif t == "user" and first_prompt is None and not d.get("isMeta"):
                txt = text_of(d.get("message", {})).strip()
                if txt and not txt.startswith("<") and not txt.startswith("Caveat:"):
                    first_prompt = re.sub(r"\s+", " ", txt)[:60]
    return first_cwd, last_cwd, title, first_prompt

def tail_valid(path):
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END); size = f.tell()
            f.seek(max(0, size - 65536))
            lines = [l for l in f.read().split(b"\n") if l.strip()]
        if not lines: return False
        json.loads(lines[-1]); return True
    except Exception:
        return False

def last_cwd_of(path):
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END); size = f.tell()
            f.seek(max(0, size - 262144))
            tail = f.read().decode("utf-8", "replace").splitlines()
        for ln in reversed(tail):
            try: d = json.loads(ln)
            except Exception: continue
            if isinstance(d, dict) and "cwd" in d: return d["cwd"]
    except Exception:
        pass
    return None

def lease_info(path):
    d = load_json(path, None)
    if not d: return None
    try:
        acq = datetime.datetime.fromisoformat(d["acquired_at"].replace("Z", "+00:00"))
        age = (datetime.datetime.now(datetime.timezone.utc) - acq).total_seconds()
        d["expired"] = age > int(d.get("ttl_minutes", TTL)) * 60
        d["age_s"] = int(age)
        return d
    except Exception:
        return None

def active_leases():
    """{session_id: lease} for fresh per-session leases, plus 'vault' for a legacy vault-wide one."""
    out = {}
    for p in glob.glob(os.path.join(VDIR, "leases", "*.json")):
        li = lease_info(p)
        if li and not li["expired"]: out[os.path.basename(p)[:-5]] = li
    legacy = lease_info(os.path.join(VDIR, "lease.json"))
    if legacy and not legacy["expired"]: out["vault"] = legacy
    return out

def build_index():
    idx_path = os.path.join(VDIR, "index.json")
    idx = load_json(idx_path, {}).get("sessions", {})
    new_idx, rows = {}, []
    for path in sorted(glob.glob(os.path.join(SDIR, "*.jsonl"))):
        sid = os.path.basename(path)[:-6]
        if not UUID_RE.match(sid): continue
        st = os.stat(path)
        cached = idx.get(sid)
        if cached and cached.get("size") == st.st_size and cached.get("mtime") == int(st.st_mtime):
            e = dict(cached)
        else:
            fc, lc, title, fp = scan(path)
            e = {"owner": user_for(fc), "last_by": user_for(lc), "name": title,
                 "first_prompt": fp, "size": st.st_size, "mtime": int(st.st_mtime)}
        e["id"] = sid
        e["torn"] = not tail_valid(path)
        e["handoff"] = os.path.exists(os.path.join(VDIR, "handoff", sid + ".done"))
        new_idx[sid] = {k: e[k] for k in ("owner", "last_by", "name", "first_prompt", "size", "mtime")}
        rows.append(e)
    try:
        with open(idx_path, "w") as f: json.dump({"sessions": new_idx}, f, indent=1)
    except Exception:
        pass
    leases = active_leases()
    for e in rows:
        li = leases.get(e["id"])
        if li:
            mine = li.get("user") == SELF_USER and li.get("host") == SELF_HOST
            e["state"] = "in use (you)" if mine else f'in use by {li.get("user","?")}'
        elif e["torn"]:
            e["state"] = "syncing"
        elif e["handoff"]:
            e["state"] = "handed off"
        else:
            e["state"] = "unknown"
    rows.sort(key=lambda e: -e["mtime"])
    return rows

BIG = int(os.environ.get("BIG_SESSION_BYTES", "5000000"))
def human_size(n):
    if n < 1024: return f"{n}B"
    if n < 1024*1024: return f"{n//1024}K"
    return f"{n/1048576:.1f}M"

def turns(path, last_n=None):
    out = []
    for raw in open(path, "rb"):
        try: d = json.loads(raw)
        except Exception: continue
        if not isinstance(d, dict) or d.get("type") not in ("user", "assistant") or d.get("isMeta"): continue
        if d.get("isCompactSummary"): out.append(("compact", text_of(d.get("message", {})).strip())); continue
        txt = text_of(d.get("message", {})).strip()
        if not txt or txt.startswith("<"): continue
        out.append((d["type"], txt))
    return out[-last_n:] if last_n else out

def rel_time(ts):
    s = int(time.time() - ts)
    if s < 60: return "just now"
    if s < 3600: return f"{s//60}m ago"
    if s < 86400: return f"{s//3600}h ago"
    if s < 7*86400: return f"{s//86400}d ago"
    return datetime.datetime.fromtimestamp(ts).strftime("%Y-%m-%d")

def print_table(rows, numbered=False, limit=None):
    if not rows:
        print(dim("  (no sessions in this vault yet)")); return
    shown = rows[:limit] if limit else rows
    w_name = max(4, min(32, max(len(e["name"] or ("(unnamed) " + e["id"][:8])) for e in shown)))
    w_state = max(5, max(len(e["state"]) for e in shown))
    w_own = max(10, max(len(e["owner"]) for e in shown))
    w_last = max(7, max(len(e["last_by"]) for e in shown))
    hdr = "   # " if numbered else ""
    hdr += f'{"NAME":<{w_name}}  {"STATE":<{w_state}}  {"STARTED-BY":<{w_own}}  {"LAST-BY":<{w_last}}  {"LAST-ACTIVE":<11}  {"SIZE":>6}  ID'
    print(dim(hdr))
    big = []
    for i, e in enumerate(shown, 1):
        nm = e["name"]
        nm_s = f"{nm[:w_name]:<{w_name}}" if nm else dim(f"{('(unnamed) ' + e['id'][:8])[:w_name]:<{w_name}}")
        stt = e["state"]
        if stt.startswith("in use"): stt_s = yel(f"{stt:<{w_state}}")
        elif stt == "handed off":    stt_s = grn(f"{stt:<{w_state}}")
        elif stt == "syncing":       stt_s = yel(f"{stt:<{w_state}}")
        else:                        stt_s = dim(f"{stt:<{w_state}}")
        line = f"  {i:>2} " if numbered else ""
        sz = human_size(e["size"])
        sz_s = yel(f"{sz:>6}") if e["size"] > BIG else f"{sz:>6}"
        if e["size"] > BIG: big.append(nm or e["id"][:8])
        line += f'{nm_s}  {stt_s}  {e["owner"]:<{w_own}}  {e["last_by"]:<{w_last}}  {rel_time(e["mtime"]):<11}  {sz_s}  {dim(e["id"][:8])}'
        print(line)
        if not nm and e.get("first_prompt"):
            print(dim(f'{"":>{5 if numbered else 0}}   "{e["first_prompt"]}"'))
    if limit and len(rows) > limit:
        print(dim(f"  … {len(rows)-limit} more — run `vault sessions` for all"))
    if big:
        print(yel(f"  ! large session(s): {', '.join(big)} — slow to sync and heavy to resume. Run /compact inside Claude before handing off."))

def resolve(arg, rows):
    """Return (sid, None) or (None, error message)."""
    if UUID_RE.match(arg):
        return (arg, None) if any(e["id"] == arg for e in rows) else (None, f"no session with id {arg} in this vault (not synced yet?)")
    exact = [e for e in rows if (e["name"] or "") == arg]
    if len(exact) == 1: return exact[0]["id"], None
    if len(exact) > 1:
        return None, "several sessions share the name '%s':\n  %s\nuse the ID instead, and `vault rename` one of them." % (
            arg, "\n  ".join(f'{e["id"]}  {e["owner"]}  {rel_time(e["mtime"])}' for e in exact))
    ci = [e for e in rows if (e["name"] or "").lower() == arg.lower()]
    if len(ci) == 1: return ci[0]["id"], None
    if len(arg) >= 4:
        pre = [e for e in rows if e["id"].startswith(arg.lower())]
        if len(pre) == 1: return pre[0]["id"], None
        if len(pre) > 1: return None, f"'{arg}' matches {len(pre)} session ids — give more characters"
    npre = [e for e in rows if (e["name"] or "").lower().startswith(arg.lower())]
    if len(npre) == 1: return npre[0]["id"], None
    if len(npre) > 1:
        return None, "'%s' matches several names: %s" % (arg, ", ".join(e["name"] for e in npre))
    return None, f"no session named '{arg}' in this vault. Run `vault sessions` to see what exists."

cmd, args = sys.argv[1], sys.argv[2:]

if cmd == "encode":
    sys.stdout.write(encode_path(args[0]))
elif cmd == "list":
    rows = build_index()
    if "--json" in args:
        print(json.dumps(rows, indent=1))
    else:
        numbered = "--numbered" in args
        limit = None
        for a in args:
            if a.startswith("--limit="): limit = int(a.split("=", 1)[1])
        print_table(rows, numbered=numbered, limit=limit)
elif cmd == "ids":
    print("\n".join(e["id"] for e in build_index()))
elif cmd == "count":
    print(len(build_index()))
elif cmd == "resolve":
    sid, err = resolve(args[0], build_index())
    if err: print(err, file=sys.stderr); sys.exit(2)
    print(sid)
elif cmd == "name-of":
    for e in build_index():
        if e["id"] == args[0]: print(e["name"] or ""); break
elif cmd == "last-by-of":
    for e in build_index():
        if e["id"] == args[0]: print(e["last_by"]); break
elif cmd == "name-taken":       # exit 0 (and print id) if a session already has this name
    for e in build_index():
        if (e["name"] or "").lower() == args[0].lower(): print(e["id"]); sys.exit(0)
    sys.exit(1)
elif cmd == "rename":           # append a custom-title record; Claude honours the LAST one
    sid, name = args[0], args[1]
    path = os.path.join(SDIR, sid + ".jsonl")
    with open(path, "a") as f:
        f.write(json.dumps({"type": "custom-title", "customTitle": name, "sessionId": sid}) + "\n")
elif cmd == "tail-valid":
    sys.exit(0 if tail_valid(args[0]) else 1)
elif cmd == "last-cwd-mine":
    lc = last_cwd_of(args[0])
    sys.exit(0 if lc and (lc == ROOT or lc.startswith(ROOT + "/")) else 1)
elif cmd == "lease-info":       # prints: user host pid acquired_at ttl expired(0/1)
    li = lease_info(args[0])
    if li:
        print(li.get("user","?"), li.get("host","?"), li.get("pid",0), li.get("acquired_at","?"), li.get("ttl_minutes",TTL), 1 if li["expired"] else 0)
elif cmd == "leases":           # sid user@host age
    for sid, li in active_leases().items():
        print(f'{sid} {li.get("user","?")}@{li.get("host","?")} {li["age_s"]//60}m')
elif cmd == "users-add":
    p = os.path.join(VDIR, "users.json")
    d = load_json(p, {"users": []}); d.setdefault("users", [])
    if not any(x.get("user")==SELF_USER and x.get("host")==SELF_HOST and x.get("vault_path")==ROOT for x in d["users"]):
        d["users"].append({"user": SELF_USER, "host": SELF_HOST, "vault_path": ROOT,
                           "joined_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")})
    json.dump(d, open(p, "w"), indent=1)
elif cmd == "users-remove":
    p = os.path.join(VDIR, "users.json")
    d = load_json(p, {"users": []})
    d["users"] = [x for x in d.get("users", []) if not (x.get("user")==SELF_USER and x.get("host")==SELF_HOST)]
    json.dump(d, open(p, "w"), indent=1)
elif cmd == "users-list":
    for u in users(): print(f'{u.get("user","?")}@{u.get("host","?")} (joined {str(u.get("joined_at",""))[:10]})')
elif cmd == "users-count":
    print(len(users()))
elif cmd == "config":
    print(load_json(os.path.join(VDIR, "config.json"), {}).get(args[0], ""))
elif cmd == "conflicts-list":
    files = sorted(glob.glob(os.path.join(VDIR, "conflicts", "*.jsonl")))
    if not files: print(dim("  (no conflict copies)")); sys.exit(0)
    names = {e["id"]: e["name"] for e in build_index()}
    for i, p in enumerate(files, 1):
        base = os.path.basename(p)[:-6]
        m = re.match(r"^([0-9a-f-]{36})-?(.*)$", base)
        sid, machine = (m.group(1), m.group(2)) if m else (base, "")
        try: n = sum(1 for _ in open(p, "rb"))
        except Exception: n = 0
        nm = names.get(sid) or sid[:8]
        print(f"  {i:>2}  {bold(nm):<24} from {machine or '?':<28} {n:>4} lines  {dim(os.path.basename(p))}")
elif cmd == "conflicts-show":   # plain-text dump of the human/assistant turns in a conflict copy
    files = sorted(glob.glob(os.path.join(VDIR, "conflicts", "*.jsonl")))
    try: p = files[int(args[0]) - 1]
    except Exception: print("no such conflict number", file=sys.stderr); sys.exit(1)
    print(dim(f"# {os.path.basename(p)}"))
    for who, txt in turns(p):
        tag = grn("human>") if who == "user" else (dim("[compacted summary]") if who == "compact" else bold("claude>"))
        print(f"{tag} {txt[:2000]}\n")
elif cmd == "show":             # last N turns of a session, as text
    sid, n = args[0], int(args[1])
    for who, txt in turns(os.path.join(SDIR, sid + ".jsonl"), n):
        tag = grn("human>") if who == "user" else (dim("[compacted summary]") if who == "compact" else bold("claude>"))
        print(f"{tag} {txt[:3000]}\n")
elif cmd == "members-memory":   # keep a shared memory file listing members, so Claude knows who may be driving
    mem = os.path.join(SDIR, "memory"); os.makedirs(mem, exist_ok=True)
    names = sorted({u.get("user","?") for u in users()})
    body = ("---\nname: vault-members\ndescription: Who shares this vault; any of them may be the person typing in a resumed session\n"
            "metadata:\n  type: project\n---\n\nThis folder is a shared team vault. Members: " + ", ".join(names) + ".\n"
            "Any member may be driving a session, and the whole conversation is shared among all members by agreement, "
            "so continue with whoever is driving using the full context. The wrapper states who is driving when a session is resumed.\n")
    open(os.path.join(mem, "vault-members.md"), "w").write(body)
    idx = os.path.join(mem, "MEMORY.md")
    cur = open(idx).read() if os.path.exists(idx) else "# Memory Index\n"
    if "vault-members.md" not in cur:
        open(idx, "w").write(cur.rstrip("\n") + "\n- [Vault members](vault-members.md) — who shares this vault; any may be driving\n")
elif cmd == "memory-conflicts": # OneDrive conflict copies inside the shared memory folder
    mem = os.path.join(SDIR, "memory")
    for p in sorted(glob.glob(os.path.join(mem, "*.md"))):
        stem = os.path.basename(p)[:-3]
        m = re.match(r"^(.+?)(?: \(\d+\)|-[A-Za-z0-9][A-Za-z0-9 ’']*)$", stem)
        if m and os.path.exists(os.path.join(mem, m.group(1) + ".md")):
            print(os.path.basename(p), m.group(1) + ".md")
else:
    print(f"vpy: unknown command {cmd}", file=sys.stderr); sys.exit(1)
PY
}

# ---------- vault discovery ----------

find_vault_root() {
  local d="$PWD"
  while [[ "$d" != "/" ]]; do
    if [[ -d "$d/$VAULT_DIRNAME/sessions" ]]; then printf '%s' "$d"; return 0; fi
    d="$(dirname "$d")"
  done
  return 1
}

load_vault() {   # sets VAULT_ROOT etc. if inside a vault; returns 1 otherwise
  VAULT_ROOT="$(find_vault_root)" || return 1
  VAULT_ROOT="$(cd "$VAULT_ROOT" && pwd -P)"     # claude encodes the realpath'd cwd
  VAULT_DIR="$VAULT_ROOT/$VAULT_DIRNAME"
  SESSIONS_DIR="$VAULT_DIR/sessions"
  export VAULT_ROOT VAULT_DIR SESSIONS_DIR
  ENC="$(vpy encode "$VAULT_ROOT")"
  LINK_PATH="$HOME/.claude/projects/$ENC"
  VAULT_NAME="$(vpy config name)"; VAULT_NAME="${VAULT_NAME:-$(basename "$VAULT_ROOT")}"
  return 0
}

require_vault() {
  load_vault && return 0
  echo "${R}✗${N} You are not inside a vault folder." >&2
  if [[ -s "$REGISTRY" ]]; then
    echo "  Vaults you have joined on this Mac:" >&2
    while IFS= read -r r; do [[ -d "$r/$VAULT_DIRNAME" ]] && echo "    cd \"$r\"" >&2; done < "$REGISTRY"
  else
    echo "  To make a shared folder a vault:   cd <shared OneDrive folder> && vault init" >&2
    echo "  To join one a teammate made:       cd <that folder> && vault join" >&2
  fi
  exit 1
}

is_joined() {
  [[ -L "$LINK_PATH" ]] && [[ "$(readlink "$LINK_PATH")" == "$SESSIONS_DIR" ]]
}

require_joined() {
  is_joined && return 0
  if [[ -L "$LINK_PATH" ]]; then
    die "your join is broken: $LINK_PATH points to '$(readlink "$LINK_PATH")' instead of this vault. Run 'vault leave --force' then 'vault join'."
  fi
  echo "${R}✗${N} You have not joined this vault yet (${B}$VAULT_NAME${N})." >&2
  hint "vault join" >&2
  exit 1
}

is_cloud_path() { [[ "$1" == "$HOME/Library/CloudStorage/"* ]]; }

provider_eval() {   # best-effort File Provider sync state (undocumented Apple tool)
  is_cloud_path "$1" || return 1
  fileproviderctl evaluate "$1" 2>/dev/null | grep -E 'isUploaded|isUploading|isMostRecentVersionDownloaded|isSyncPaused|isKeepDownloaded|isDownloaded' || true
}

uuid_re='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
name_re='^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$'
now_iso() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }
new_uuid() { uuidgen | tr 'A-Z' 'a-z'; }

registry_add()    { mkdir -p "$STATE_DIR"; touch "$REGISTRY"; grep -qxF "$VAULT_ROOT" "$REGISTRY" || echo "$VAULT_ROOT" >> "$REGISTRY"; }
registry_remove() { if [[ -f "$REGISTRY" ]]; then grep -vxF "$VAULT_ROOT" "$REGISTRY" > "$REGISTRY.tmp" || true; mv "$REGISTRY.tmp" "$REGISTRY"; fi; return 0; }
conflict_count()  { find "$VAULT_DIR/conflicts" -name '*.jsonl' 2>/dev/null | wc -l | tr -d ' '; }

# ---------- preflight ----------

check_onedrive() {
  is_cloud_path "$VAULT_ROOT" || return 0
  if ! pgrep -x OneDrive >/dev/null 2>&1; then
    warn "OneDrive is not running — nothing you do here will reach your teammates until it starts."
  fi
  if provider_eval "$VAULT_DIR" | grep -q 'isSyncPaused = 1'; then
    warn "OneDrive sync is PAUSED — resume it from the menu-bar icon or nothing will propagate."
  fi
}

# OneDrive conflict copies keep .jsonl but their basename is no longer a pure
# UUID (e.g. '3f2a…-Shash’s MacBook Pro.jsonl'). The loser's turns exist ONLY
# there; quarantine so the native /resume picker can't grab them.
quarantine_conflicts() {
  local f base dest
  for f in "$SESSIONS_DIR"/*.jsonl; do
    [[ -e "$f" ]] || continue
    base="$(basename "$f" .jsonl)"
    if [[ ! "$base" =~ $uuid_re ]]; then
      mkdir -p "$VAULT_DIR/conflicts"
      dest="$VAULT_DIR/conflicts/$(basename "$f")"
      [[ -e "$dest" ]] && dest="$VAULT_DIR/conflicts/$base.$(date +%s).jsonl"
      mv "$f" "$dest"
      warn "two people were in one session at the same time. OneDrive kept both copies;"
      warn "the extra one is now in .vault/conflicts/ — its turns are NOT in the main session."
      hint "vault conflicts        to see and read what was lost" >&2
    fi
  done
  return 0
}

check_drift() {
  mkdir -p "$STATE_DIR"
  local state="$STATE_DIR/$ENC.drift" f h line changed=() first_run=0 new=""
  local tracked=("$VAULT_ROOT/CLAUDE.md" "$VAULT_ROOT/.claude/settings.json" "$VAULT_ROOT/.claude/settings.local.json")
  [[ -f "$state" ]] || first_run=1
  for f in "${tracked[@]}"; do
    if [[ -f "$f" ]]; then h="$(md5 -q "$f" 2>/dev/null || md5sum "$f" | cut -d' ' -f1)"; else h="absent"; fi
    new+="$f $h"$'\n'
    if [[ -f "$state" ]]; then
      line="$(grep -F "$f " "$state" 2>/dev/null || true)"
      [[ -n "$line" && "$line" != "$f $h" ]] && changed+=("$f")
    elif [[ "$h" != "absent" ]]; then
      changed+=("$f")
    fi
  done
  printf '%s' "$new" > "$state"
  if [[ ${#changed[@]} -gt 0 ]]; then
    if [[ "$first_run" == "1" ]]; then
      warn "this vault already has shared instruction/permission files you have never reviewed:"
    else
      warn "shared instruction/permission files changed since your last run (a teammate edited them):"
    fi
    for f in "${changed[@]}"; do echo "    ${f#"$VAULT_ROOT"/}" >&2; done
    warn "they steer Claude and grant permissions on YOUR machine — skim them before working. Never 'always allow' in a vault."
  fi
}

check_memory_conflicts() {
  local line
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    warn "shared memory has a conflict copy: ${line% *} duplicates ${line#* } — two people's Claude wrote memory at once. Merge by hand in .vault/sessions/memory/."
  done < <(vpy memory-conflicts)
}

# Bypass-permissions inside a vault = any member's CLAUDE.md can run anything on your Mac.
check_bypass_flags() {
  local a prev=""
  for a in "$@"; do
    if [[ "$a" == "--dangerously-skip-permissions" || ( "$prev" == "--permission-mode" && "$a" == "bypassPermissions" ) || "$a" == "--permission-mode=bypassPermissions" ]]; then
      die "'$a' is not allowed inside a vault: the shared CLAUDE.md and settings here are editable by every member,
  so bypassing permissions would let any member run anything on your machine. Use 'vault private' for that."
    fi
    prev="$a"
  done
}
check_bypass_settings() {
  local f
  for f in "$VAULT_ROOT/.claude/settings.json" "$VAULT_ROOT/.claude/settings.local.json"; do
    [[ -f "$f" ]] || continue
    if grep -Eq '"defaultMode"[[:space:]]*:[[:space:]]*"bypassPermissions"' "$f"; then
      die "refusing to start: ${f#"$VAULT_ROOT"/} sets defaultMode to bypassPermissions.
  In a shared vault that means any member could make Claude run anything on your machine.
  Remove that line (and ask who added it) before working here."
    fi
  done
}

preflight() { check_onedrive; quarantine_conflicts; check_drift; check_memory_conflicts; check_bypass_settings; }

# ---------- leases (per session, advisory) ----------

lease_path() { printf '%s' "$VAULT_DIR/leases/$1.json"; }

acquire_lease() {   # $1 = session id; honours $STEAL
  local sid="$1" li l_user l_host l_pid l_at l_ttl l_exp
  mkdir -p "$VAULT_DIR/leases"
  # legacy vault-wide lease from v0.1 clients
  if [[ -f "$VAULT_DIR/lease.json" ]]; then
    li="$(vpy lease-info "$VAULT_DIR/lease.json")"
    if [[ -n "$li" ]]; then
      read -r l_user l_host l_pid l_at l_ttl l_exp <<< "$li"
      if [[ "$l_exp" == "0" && ( "$l_user" != "$SELF_USER" || "$l_host" != "$SELF_HOST" ) && "${STEAL:-0}" != "1" ]]; then
        die "$l_user@$l_host is working in this vault with an older vault version (since $l_at).
  Ask them to finish and to run 'vault update'. If you are sure they are done:   re-run with --steal"
      fi
    fi
  fi
  if [[ -f "$(lease_path "$sid")" ]]; then
    li="$(vpy lease-info "$(lease_path "$sid")")"
    if [[ -n "$li" ]]; then
      read -r l_user l_host l_pid l_at l_ttl l_exp <<< "$li"
      local other=0
      if [[ "$l_user" != "$SELF_USER" || "$l_host" != "$SELF_HOST" ]]; then other=1
      elif [[ "$l_pid" != "$$" ]] && kill -0 "$l_pid" 2>/dev/null; then other=1; fi
      if [[ "$l_exp" == "0" && "$other" == "1" ]]; then
        if [[ "${STEAL:-0}" == "1" ]]; then
          warn "taking over the session from $l_user@$l_host (they opened it $l_at)"
        else
          die "$l_user@$l_host is in this session right now (opened $l_at).
  Two people in one session at once forks it and loses turns.
  Ask them to exit, or if you are SURE they are done:   re-run with --steal"
        fi
      fi
    fi
  fi
  local tmp="$VAULT_DIR/leases/.$sid.$$"
  printf '{"user":"%s","host":"%s","pid":%d,"session_id":"%s","acquired_at":"%s","ttl_minutes":%d}\n' \
    "$SELF_USER" "$SELF_HOST" "$$" "$sid" "$(now_iso)" "$LEASE_TTL_MINUTES" > "$tmp"
  mv "$tmp" "$(lease_path "$sid")"
  LEASE_SID="$sid"
}

release_lease() {
  [[ -n "${LEASE_SID:-}" ]] || return 0
  local li l_user l_host l_pid
  li="$(vpy lease-info "$(lease_path "$LEASE_SID")" 2>/dev/null || true)"
  if [[ -n "$li" ]]; then
    read -r l_user l_host l_pid _ _ _ <<< "$li"
    [[ "$l_user" == "$SELF_USER" && "$l_host" == "$SELF_HOST" && "$l_pid" == "$$" ]] && rm -f "$(lease_path "$LEASE_SID")"
  fi
  LEASE_SID=""
  return 0
}

# ---------- handoff ----------

finish_handoff() {   # $1 = start marker; marks every session THIS run touched as handed off
  local start_marker="$1" f sid waited touched=0 nm
  mkdir -p "$VAULT_DIR/handoff"
  while IFS= read -r f; do
    sid="$(basename "$f" .jsonl)"
    [[ "$sid" =~ $uuid_re ]] || continue
    vpy last-cwd-mine "$f" || continue      # a teammate's session syncing in mid-run is not ours
    printf '{"user":"%s","host":"%s","released_at":"%s"}\n' "$SELF_USER" "$SELF_HOST" "$(now_iso)" \
      > "$VAULT_DIR/handoff/$sid.done"
    touched=1
    nm="$(vpy name-of "$sid")"
    if is_cloud_path "$f"; then
      waited=0
      printf '  uploading %s to OneDrive…' "${nm:-${sid:0:8}}"
      while (( waited < UPLOAD_WAIT_SECS )); do
        provider_eval "$f" | grep -q 'isUploading = 1' || break
        sleep 3; waited=$((waited+3)); printf '.'
      done
      echo
    fi
    ok "session ${B}${nm:-${sid:0:8}}${N} handed off — teammates can resume it once OneDrive delivers it."
    if [[ -n "$nm" ]]; then hint "they run:  vault resume \"$nm\""; else hint "they run:  vault resume ${sid:0:8}   (give it a name: vault rename ${sid:0:8} <name>)"; fi
  done < <(find "$SESSIONS_DIR" -maxdepth 1 -name '*.jsonl' -newer "$start_marker" 2>/dev/null)
  [[ "$touched" == "1" ]] || info "${D}(no shared session was changed in this run)${N}"
}

start_heartbeat() {   # refresh our lease while Claude runs, so long sessions never look free
  local sid="$1" parent=$$
  # No traps (bash 3.2 on macOS won't kill a sleeping child from one). Instead: short
  # sleep slices, and exit as soon as the parent is gone or the lease isn't ours.
  (
    n=0; t=0; slice=2; (( LEASE_HEARTBEAT_SECS < slice )) && slice=$LEASE_HEARTBEAT_SECS
    while kill -0 "$parent" 2>/dev/null; do
      sleep "$slice"; t=$((t+slice))
      (( t >= LEASE_HEARTBEAT_SECS )) || continue
      t=0
      [[ -f "$(lease_path "$sid")" ]] || exit 0
      grep -q "\"pid\":$parent," "$(lease_path "$sid")" || exit 0
      n=$((n+1))
      printf '{"user":"%s","host":"%s","pid":%d,"session_id":"%s","acquired_at":"%s","ttl_minutes":%d,"heartbeats":%d}\n' \
        "$SELF_USER" "$SELF_HOST" "$parent" "$sid" "$(now_iso)" "$LEASE_TTL_MINUTES" "$n" > "$VAULT_DIR/leases/.$sid.hb.$parent"
      mv "$VAULT_DIR/leases/.$sid.hb.$parent" "$(lease_path "$sid")"
    done
  ) >/dev/null 2>&1 </dev/null &
  HEARTBEAT_PID=$!
}
stop_heartbeat() {
  if [[ -n "${HEARTBEAT_PID:-}" ]]; then kill "$HEARTBEAT_PID" 2>/dev/null || true; wait "$HEARTBEAT_PID" 2>/dev/null || true; fi
  HEARTBEAT_PID=""; return 0
}

launch_claude() {   # $1 = session id (lease), rest = claude args. Handles lease/marker lifecycle.
  local sid="$1"; shift
  check_bypass_flags "$@"
  acquire_lease "$sid"
  start_heartbeat "$sid"
  trap 'stop_heartbeat; release_lease' EXIT INT TERM
  local start_marker="$VAULT_DIR/.run-start.$$"
  touch "$start_marker"
  rm -f "$VAULT_DIR/handoff/$sid.done"     # live again — the old clean-exit marker is no longer true
  local rc=0
  ( cd "$VAULT_ROOT" && claude "$@" ) || rc=$?
  echo
  stop_heartbeat
  finish_handoff "$start_marker"
  rm -f "$start_marker"
  release_lease
  trap - EXIT INT TERM
  return $rc
}

reorient_prompt() {
  printf '%s' "This session lives in a shared team vault ('$VAULT_NAME'). All members of the vault are co-owners of this session and act as one principal: every member has agreed that the entire conversation, including everything said or produced in earlier turns, is shared among all members, and a different member taking over the keyboard is expected and authorized by all of them. There is no per-person confidentiality inside a vault session; do not withhold earlier content from the current driver on the grounds that another member provided it. The member now driving is $SELF_USER; earlier turns may have been driven by other members. Continue the work with them exactly as you would with the original person, using the full context. The vault root on THIS machine is: $VAULT_ROOT — re-resolve any absolute file paths from earlier in the conversation relative to this root before using them."
}

# ---------- commands ----------

cmd_init() {
  local name="${1:-}"
  if load_vault 2>/dev/null; then
    ok "this folder is already a vault (${B}$VAULT_NAME${N})."
    if is_joined; then hint "vault              to see what's here"; else hint "vault join"; fi
    return 0
  fi
  local root; root="$(pwd -P)"
  name="${name:-$(basename "$root")}"
  if ! is_cloud_path "$root"; then
    warn "this folder is not under ~/Library/CloudStorage — it is not a synced OneDrive folder."
    warn "a vault only shares if the folder syncs to your teammates. Continuing anyway (fine for testing)."
  fi
  mkdir -p "$root/$VAULT_DIRNAME"/{sessions/memory,handoff,conflicts,leases}
  printf '{"vault_id":"%s","name":"%s","created_by":"%s@%s","created_at":"%s","schema":1}\n' \
    "$(new_uuid)" "$name" "$SELF_USER" "$SELF_HOST" "$(now_iso)" > "$root/$VAULT_DIRNAME/config.json"
  printf '{"users":[]}\n' > "$root/$VAULT_DIRNAME/users.json"
  printf '{"sessions":{}}\n' > "$root/$VAULT_DIRNAME/index.json"
  cat > "$root/$VAULT_DIRNAME/sessions/memory/MEMORY.md" <<'EOF'
# Memory Index

- NOTE: this is SHARED VAULT MEMORY. Everything here is visible to and writable
  by every member of this vault, and is injected into every member's sessions.
  Do not store personal facts here.
EOF
  ok "created vault ${B}$name${N} at $root"
  echo
  cmd_join
}

cmd_join() {
  require_vault
  mkdir -p "$HOME/.claude/projects" "$STATE_DIR"
  if [[ -L "$LINK_PATH" ]]; then
    if [[ "$(readlink "$LINK_PATH")" == "$SESSIONS_DIR" ]]; then
      registry_add
      ok "already joined ${B}$VAULT_NAME${N}."
      hint "vault              to see the sessions here"
      return 0
    fi
    die "something else is linked at $LINK_PATH ($(readlink "$LINK_PATH")). Run 'vault leave --force' there first."
  fi
  if [[ -d "$LINK_PATH" ]]; then
    mkdir -p "$BACKUP_DIR"
    local backup="$BACKUP_DIR/$ENC.$(date +%s)"
    mv "$LINK_PATH" "$backup"
    info "you had private Claude sessions for this folder from before joining — they were moved aside"
    info "(not shared, not deleted): $backup"
  fi
  ln -s "$SESSIONS_DIR" "$LINK_PATH"
  vpy users-add
  vpy members-memory
  registry_add

  # Empirical self-test: Claude's path encoding is undocumented and version-specific.
  # Launch once with a nonce and confirm the transcript landed in the vault.
  printf '  checking that Claude really uses the shared folder (one silent Claude call)…'
  local nonce marker found="" stray="" sid f d g
  nonce="$(new_uuid)"; marker="$STATE_DIR/.join-selftest.$$"; touch "$marker"
  ( cd "$VAULT_ROOT" && claude -p "vault join self-test $nonce — reply OK" >/dev/null 2>&1 ) || true
  while IFS= read -r f; do
    grep -q "$nonce" "$f" 2>/dev/null && { found="$f"; break; }
  done < <(find "$SESSIONS_DIR" -maxdepth 1 -name '*.jsonl' -newer "$marker" 2>/dev/null)
  if [[ -z "$found" ]]; then
    while IFS= read -r d; do
      g="$(grep -l "$nonce" "$d"/*.jsonl 2>/dev/null | head -1 || true)"
      [[ -n "$g" ]] && { stray="$g"; break; }
    done < <(find "$HOME/.claude/projects" -maxdepth 1 -type d -newer "$marker" 2>/dev/null)
  fi
  rm -f "$marker"; echo
  if [[ -n "$found" ]]; then
    sid="$(basename "$found" .jsonl)"
    rm -f "$found" "$VAULT_DIR/handoff/$sid.done"; rm -rf "$SESSIONS_DIR/$sid"
    ok "joined ${B}$VAULT_NAME${N} as $SELF_USER@$SELF_HOST (verified: Claude writes into the vault)"
  elif [[ -n "$stray" ]]; then
    rm -f "$LINK_PATH"; vpy users-remove; registry_remove
    die "join FAILED: this Claude version stores sessions somewhere the vault does not expect
  ($stray). Nothing was joined. Please report this at https://github.com/shashb27/vault/issues
  with your 'claude --version'."
  else
    warn "could not verify (is Claude logged in? run 'claude' once by itself). Joined, but unproven —"
    warn "after your first 'vault new' check that the session shows up in 'vault sessions'."
    ok "joined ${B}$VAULT_NAME${N} as $SELF_USER@$SELF_HOST"
  fi

  echo
  echo "  ${B}Before you start — the three things to know${N}"
  echo "  1. Everything Claude reads or prints in a vault session is shared with every member."
  echo "     Don't cat secrets or pull personal mail/calendar into a vault session."
  echo "  2. CLAUDE.md, .claude/settings and Claude's memory in this folder are shared and editable"
  echo "     by every member — they steer Claude on YOUR machine. Never click 'don't ask again' here."
  echo "  3. One person per session at a time. Exit Claude when you're done so it hands off cleanly."
  echo "  ${D}Full trust model: docs/safety.md in the vault repo.${N}"
  if is_cloud_path "$VAULT_ROOT"; then
    echo
    if ! provider_eval "$VAULT_DIR" | grep -q 'isKeepDownloaded = 1'; then
      warn "one manual step macOS won't let us script: in Finder, right-click the '$VAULT_DIRNAME' folder"
      warn "here and choose ${B}Always Keep on This Device${N} (press Cmd+Shift+. to show hidden folders)."
    fi
    find "$SESSIONS_DIR" -type f -exec cat {} + >/dev/null 2>&1 || true   # materialize what exists today
  fi
  echo
  local n; n="$(vpy count)"
  if [[ "$n" == "0" ]]; then
    hint "vault new <name>        start the first shared session, e.g.  vault new planning-review"
  else
    hint "vault                   see the $n session(s) already here, then 'vault resume <name>'"
  fi
}

cmd_new() {
  require_vault; require_joined
  local name="" args=()
  STEAL=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --steal) STEAL=1; shift ;;
      --private) shift; cmd_private "$@"; return $? ;;
      -*) args+=("$1"); shift ;;
      *) if [[ -z "$name" ]]; then name="$1"; else args+=("$1"); fi; shift ;;
    esac
  done
  if [[ -z "$name" ]]; then
    echo "Every shared session needs a name so teammates can find it (e.g. ${B}planning-review${N}, ${B}q4-budget${N})."
    read -r -p "  name for this session: " name
    [[ -n "$name" ]] || die "no name given."
  fi
  [[ "$name" =~ $name_re ]] || die "'$name' is not a valid name: use letters, digits, '-', '_', '.', spaces (max 64 chars)."
  local existing
  if existing="$(vpy name-taken "$name")"; then
    die "a session named '${B}$name${N}' already exists in this vault (${existing:0:8}).
  Continue it instead:   vault resume \"$name\"
  or pick another name:  vault new \"$name-2\""
  fi
  preflight
  local sid; sid="$(new_uuid)"
  echo "${G}▶${N} starting shared session ${B}$name${N} in vault ${B}$VAULT_NAME${N}"
  echo "  ${D}exit Claude (Ctrl+D or /exit) when you're done — that hands the session to the team.${N}"
  echo
  launch_claude "$sid" --session-id "$sid" --name "$name" ${args[@]+"${args[@]}"}
}

cmd_resume() {
  require_vault; require_joined
  local target="" args=() user_asp="" force=0 wait_secs="$GATE_WAIT_SECS"
  STEAL=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --steal) STEAL=1; force=1; shift ;;
      --force|--now) force=1; wait_secs=0; shift ;;
      --wait) wait_secs="${2:-90}"; shift 2 ;;
      --append-system-prompt) user_asp="${2:-}"; shift 2 ;;
      -*) args+=("$1"); shift ;;
      *) if [[ -z "$target" ]]; then target="$1"; else args+=("$1"); fi; shift ;;
    esac
  done
  preflight
  local sid pick
  if [[ -z "$target" ]]; then
    local n; n="$(vpy count)"
    if [[ "$n" == "0" ]]; then echo "No sessions in this vault yet."; hint "vault new <name>"; return 0; fi
    echo "${B}Sessions in $VAULT_NAME${N}"
    vpy list --numbered
    echo
    read -r -p "  resume which? (number or name, Enter to cancel): " pick
    [[ -n "$pick" ]] || return 0
    if [[ "$pick" =~ ^[0-9]+$ ]]; then
      sid="$(vpy ids | sed -n "${pick}p")"
      [[ -n "$sid" ]] || die "no session #$pick"
    else
      sid="$(vpy resolve "$pick")" || exit 1
    fi
  else
    sid="$(vpy resolve "$target")" || exit 1
  fi
  local f="$SESSIONS_DIR/$sid.jsonl" nm; nm="$(vpy name-of "$sid")"
  local label="${nm:-${sid:0:8}}"
  if is_cloud_path "$f"; then cat "$f" >/dev/null 2>&1 || true; fi   # materialize

  # ---- sync gate: wait for a complete transcript + a clean handoff, then go ----
  local mine=0; vpy last-cwd-mine "$f" && mine=1
  local waited=0 torn=1 handed=0 shown=0
  while :; do
    torn=1; vpy tail-valid "$f" && torn=0
    handed=0; [[ -f "$VAULT_DIR/handoff/$sid.done" ]] && handed=1
    if [[ "$torn" == "0" && ( "$handed" == "1" || "$mine" == "1" || "$force" == "1" ) ]]; then break; fi
    (( waited >= wait_secs )) && break
    if [[ "$shown" == "0" ]]; then
      if [[ "$torn" == "1" ]]; then printf '  waiting for OneDrive to finish delivering %s' "$label"
      else printf '  waiting for the previous person to exit %s' "$label"; fi
      shown=1
    fi
    printf '.'; sleep 5; waited=$((waited+5))
  done
  [[ "$shown" == "1" ]] && echo
  if [[ "$torn" == "1" ]]; then
    die "the transcript for '$label' is still incomplete on this Mac (OneDrive mid-sync).
  Try again in a minute:   vault resume \"$label\"
  Check progress with:     vault status \"$label\""
  fi
  if [[ "$handed" == "0" && "$mine" == "0" && "$force" == "0" ]]; then
    local last_by; last_by="$(vpy last-by-of "$sid")"; last_by="${last_by:-someone}"
    # No marker usually means Claude was started without the wrapper. If nobody holds a
    # lease and the transcript has been idle a while, treat it as finished.
    local idle=$(( $(date +%s) - $(stat -f %m "$f") ))
    if [[ ! -f "$(lease_path "$sid")" ]] && (( idle > IDLE_OK_SECS )); then
      info "${D}no clean-exit marker (started outside vault?) but idle for $((idle/60))m — treating it as finished.${N}"
      handed=1
    fi
  fi
  if [[ "$handed" == "0" && "$mine" == "0" && "$force" == "0" ]]; then
    warn "'$label' was last touched by $last_by and has no clean-exit marker yet."
    warn "either they are still in it, or their exit hasn't synced. If you both type, the session forks."
    if [[ -t 0 ]]; then
      local yn; read -r -p "  continue anyway? [y/N] " yn
      [[ "$yn" =~ ^[Yy]$ ]] || { echo "  ok — try again in a minute, or ask $last_by."; return 1; }
    else
      die "refusing without a terminal; pass --force to override."
    fi
  fi

  local reorient; reorient="$(reorient_prompt)"
  [[ -n "$user_asp" ]] && reorient="$reorient"$'\n\n'"$user_asp"
  echo "${G}▶${N} resuming ${B}$label${N} — the whole conversation so far is in context."
  echo "  ${D}exit Claude (Ctrl+D or /exit) when you're done to hand it back.${N}"
  echo
  launch_claude "$sid" --resume "$sid" --append-system-prompt "$reorient" ${args[@]+"${args[@]}"}
}

cmd_sessions() {
  require_vault
  if [[ "${1:-}" == "--json" ]]; then vpy list --json; return 0; fi
  quarantine_conflicts
  echo "${B}Sessions in $VAULT_NAME${N}  ${D}($(vpy users-count) members)${N}"
  vpy list
  echo
  echo "${D}  STARTED-BY / LAST-BY come from transcript contents — a best guess, not a verified identity.${N}"
  local n; n="$(conflict_count)"
  [[ "$n" == "0" ]] || warn "$n conflict cop(ies) exist — turns in them are missing from the sessions above. See 'vault conflicts'."
  hint "vault resume <name>     continue one   ·   vault new <name>     start another"
}

cmd_rename() {
  require_vault; require_joined
  local from="${1:-}" to="${2:-}"
  [[ -n "$from" && -n "$to" ]] || die "usage: vault rename <name-or-id> <new-name>"
  [[ "$to" =~ $name_re ]] || die "'$to' is not a valid name: letters, digits, '-', '_', '.', spaces (max 64)."
  local sid; sid="$(vpy resolve "$from")" || exit 1
  local taken
  if taken="$(vpy name-taken "$to")" && [[ "$taken" != "$sid" ]]; then
    die "another session is already named '$to' (${taken:0:8})."
  fi
  local li l_user l_host l_pid l_at l_ttl l_exp
  if [[ -f "$(lease_path "$sid")" ]]; then
    li="$(vpy lease-info "$(lease_path "$sid")")"
    if [[ -n "$li" ]]; then
      read -r l_user l_host l_pid l_at l_ttl l_exp <<< "$li"
      [[ "$l_exp" == "1" ]] || die "$l_user@$l_host is in that session right now — rename it after they exit (or use /rename inside Claude)."
    fi
  fi
  vpy tail-valid "$SESSIONS_DIR/$sid.jsonl" || die "that transcript is still syncing — try again in a minute."
  local old; old="$(vpy name-of "$sid")"
  vpy rename "$sid" "$to"
  ok "renamed ${old:-${sid:0:8}} → ${B}$to${N}"
  hint "vault resume \"$to\""
}

cmd_status() {
  require_vault
  local target="${1:-}"
  echo "${B}Vault${N}      $VAULT_NAME"
  echo "${B}Folder${N}     $VAULT_ROOT"
  echo "${B}Members${N}    $(vpy users-list | paste -sd '|' - | sed 's/|/ · /g')"
  if is_joined; then echo "${B}You${N}        joined as $SELF_USER@$SELF_HOST"; else echo "${B}You${N}        ${R}not joined${N} — run 'vault join'"; fi
  if is_cloud_path "$VAULT_ROOT"; then
    local od="running"; pgrep -x OneDrive >/dev/null 2>&1 || od="${R}NOT RUNNING${N}"
    local st; st="$(provider_eval "$VAULT_DIR" || true)"
    if echo "$st" | grep -q 'isSyncPaused = 1'; then od="$od, ${R}sync PAUSED${N}"; fi
    if echo "$st" | grep -q 'isKeepDownloaded = 1'; then od="$od, pinned"; else od="$od, ${Y}not pinned${N} (Finder → Always Keep on This Device)"; fi
    echo "${B}OneDrive${N}   $od"
  else
    echo "${B}OneDrive${N}   n/a — folder is not under ~/Library/CloudStorage (local-only vault)"
  fi
  local leases; leases="$(vpy leases)"
  if [[ -n "$leases" ]]; then
    local s w a nm line=""
    while read -r s w a; do nm="$(vpy name-of "$s" 2>/dev/null || true)"; line+="${nm:-${s:0:8}} by $w ($a)  "; done <<< "$leases"
    echo "${B}In use${N}     $line"
  else
    echo "${B}In use${N}     nobody right now"
  fi
  quarantine_conflicts
  local n; n="$(conflict_count)"
  if [[ "$n" == "0" ]]; then echo "${B}Conflicts${N}  none"; else echo "${B}Conflicts${N}  ${Y}$n quarantined${N} — see 'vault conflicts'"; fi
  check_drift; check_memory_conflicts
  if [[ -n "$target" ]]; then
    local sid; sid="$(vpy resolve "$target")" || exit 1
    local f="$SESSIONS_DIR/$sid.jsonl" nm; nm="$(vpy name-of "$sid")"
    echo
    echo "${B}Session${N}    ${nm:-(unnamed)}  ${D}$sid${N}"
    echo "  present    yes ($(stat -f '%z bytes, modified %Sm' "$f"))"
    local complete=0 handed=0
    if vpy tail-valid "$f"; then complete=1; echo "  transcript ${G}complete${N}"; else echo "  transcript ${Y}incomplete — OneDrive still delivering it${N}"; fi
    if [[ -f "$VAULT_DIR/handoff/$sid.done" ]]; then handed=1; echo "  handoff    ${G}clean${N} ($(cat "$VAULT_DIR/handoff/$sid.done"))"
    else echo "  handoff    ${Y}no clean-exit marker${N} — last person may still be in it"; fi
    if is_cloud_path "$f"; then provider_eval "$f" | sed 's/^/  onedrive   /'; fi
    echo
    if [[ "$complete" == "1" && "$handed" == "1" ]]; then hint "vault resume \"${nm:-$sid}\"     — safe to continue"
    else hint "vault resume \"${nm:-$sid}\"     — it will wait for sync and ask before proceeding"; fi
  fi
}

cmd_show() {
  require_vault
  local target="${1:-}" n="${2:-10}"
  [[ -n "$target" ]] || die "usage: vault show <name> [turns]   — read the last turns before you resume"
  local sid; sid="$(vpy resolve "$target")" || exit 1
  local nm; nm="$(vpy name-of "$sid")"
  local f="$SESSIONS_DIR/$sid.jsonl"
  if is_cloud_path "$f"; then cat "$f" >/dev/null 2>&1 || true; fi
  echo "${B}${nm:-${sid:0:8}}${N}  ${D}last $n turns · started by $(vpy list --json | python3 -c 'import json,sys; sid=sys.argv[1]; print(next((e["owner"] for e in json.load(sys.stdin) if e["id"]==sid),"?"))' "$sid")${N}"
  echo
  vpy show "$sid" "$n"
  hint "vault resume \"${nm:-$sid}\""
}

cmd_conflicts() {
  require_vault
  quarantine_conflicts
  if [[ "${1:-}" == "show" && -n "${2:-}" ]]; then vpy conflicts-show "$2"; return 0; fi
  echo "${B}Conflict copies in $VAULT_NAME${N}"
  echo "${D}  Each is a branch of a session that two people were in at the same time. Its turns are"
  echo "  NOT in the main session. Read one with 'vault conflicts show <#>' and paste anything"
  echo "  important back into a live session. Prevention: one person per session; exit when done.${N}"
  vpy conflicts-list
}

cmd_doctor() {
  local fails=0
  chk() { # $1 ok(0/1) $2 label $3 fix
    if [[ "$1" == "0" ]]; then ok "$2"; else echo "${R}✗${N} $2"; [[ -n "${3:-}" ]] && echo "    ${D}fix:${N} $3"; fails=$((fails+1)); fi
    return 0
  }
  echo "${B}vault doctor${N}  ${D}(vault $VAULT_VERSION at $INSTALL_DIR)${N}"
  local rc
  rc=0; [[ "$(uname)" == "Darwin" ]] || rc=1; chk $rc "macOS" "vault is macOS-only for now"
  local cv; cv="$(claude --version 2>/dev/null | head -1 || true)"
  rc=0; [[ -n "$cv" ]] || rc=1; chk $rc "Claude Code installed ${D}${cv}${N}" "install Claude Code and run 'claude' once to log in"
  rc=0; command -v python3 >/dev/null || rc=1; chk $rc "python3 available" "xcode-select --install"
  if load_vault; then
    chk 0 "inside vault ${B}$VAULT_NAME${N} ${D}$VAULT_ROOT${N}"
    if is_joined; then chk 0 "joined — Claude sessions started here go to the shared folder"
    else chk 1 "not joined" "vault join"; fi
    if is_cloud_path "$VAULT_ROOT"; then
      rc=0; pgrep -x OneDrive >/dev/null 2>&1 || rc=1; chk $rc "OneDrive running" "open OneDrive"
      local st; st="$(provider_eval "$VAULT_DIR" || true)"
      rc=0; echo "$st" | grep -q 'isSyncPaused = 1' && rc=1; chk $rc "OneDrive sync not paused" "resume sync from the OneDrive menu-bar icon"
      rc=0; echo "$st" | grep -q 'isKeepDownloaded = 1' || rc=1; chk $rc "vault folder pinned (Always Keep on This Device)" "Finder → right-click .vault → Always Keep on This Device"
    else
      warn "folder is not under ~/Library/CloudStorage — sessions will not reach anyone else"
    fi
    local n; n="$(conflict_count)"
    rc=0; [[ "$n" == "0" ]] || rc=1; chk $rc "no conflict copies ($n)" "vault conflicts"
    local leases; leases="$(vpy leases)"
    [[ -z "$leases" ]] || info "${Y}in use:${N} $(echo "$leases" | awk '{print $1" by "$2}' | tr '\n' ' ')"
    check_drift
  else
    chk 1 "inside a vault folder" "cd into a shared OneDrive folder, then 'vault init' or 'vault join'"
  fi
  if git -C "$INSTALL_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    ok "installed from git ${D}$(git -C "$INSTALL_DIR" rev-parse --short HEAD) $(git -C "$INSTALL_DIR" log -1 --format=%cd --date=short)${N} — 'vault update' pulls the latest"
  fi
  echo
  if [[ "$fails" == "0" ]]; then ok "${B}all good${N}"; else warn "$fails problem(s) above"; return 1; fi
}

cmd_private() {
  require_vault
  local scratch="$HOME/.claude/vault-private/$(basename "$VAULT_ROOT")"
  mkdir -p "$scratch"
  echo "${G}▶${N} private session — ${B}not shared${N} with the vault (runs from a local scratch folder)"
  ( cd "$scratch" && exec claude "$@" )
}

cmd_leave() {
  require_vault
  local force=0; [[ "${1:-}" == "--force" ]] && force=1
  [[ -L "$LINK_PATH" ]] || die "you are not joined to this vault."
  if [[ "$force" != "1" ]]; then
    local mine; mine="$(vpy leases | awk -v u="$SELF_USER@$SELF_HOST" '$2 == u' || true)"
    [[ -z "$mine" ]] || die "you are still in a session here — exit Claude first, or 'vault leave --force'."
  fi
  rm "$LINK_PATH"
  local latest; latest="$(ls -1dt "$BACKUP_DIR/$ENC."* 2>/dev/null | head -1 || true)"
  if [[ -n "$latest" ]]; then mv "$latest" "$LINK_PATH"; info "restored your pre-join private sessions from $latest"; fi
  vpy users-remove 2>/dev/null || true
  vpy members-memory 2>/dev/null || true
  registry_remove
  ok "left ${B}$VAULT_NAME${N}. Claude in this folder is private again."
  warn "leaving does not un-share: sessions you ran while joined stay in the vault, on teammates' Macs and in OneDrive history."
}

cmd_update() {
  if git -C "$INSTALL_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    local before after
    before="$(git -C "$INSTALL_DIR" rev-parse --short HEAD)"
    git -C "$INSTALL_DIR" pull --ff-only -q || die "update failed (local changes in $INSTALL_DIR?)."
    after="$(git -C "$INSTALL_DIR" rev-parse --short HEAD)"
    if [[ "$before" == "$after" ]]; then ok "already up to date ($after)"; else ok "updated $before → $after"; fi
    "$INSTALL_DIR/vault.sh" version
  else
    die "vault was not installed from git ($INSTALL_DIR). Re-run the installer from https://github.com/shashb27/vault"
  fi
}

cmd_home() {   # bare `vault`: where am I, what can I do
  if ! load_vault; then
    echo "${B}vault${N} $VAULT_VERSION — shared Claude Code sessions in a shared folder"
    echo
    echo "You are not inside a vault folder."
    if [[ -s "$REGISTRY" ]]; then
      echo "Vaults you have joined on this Mac:"
      while IFS= read -r r; do [[ -d "$r/$VAULT_DIRNAME" ]] && echo "  cd \"$r\""; done < "$REGISTRY"
      echo
      hint "cd into one of them, then run 'vault' again"
    else
      echo
      echo "  Create one:   cd <a shared OneDrive folder>   &&  vault init"
      echo "  Join one:     cd <the folder a teammate made>  &&  vault join"
      echo
      echo "${D}  'vault help' lists every command. 'vault doctor' checks your setup.${N}"
    fi
    return 0
  fi
  echo "${B}$VAULT_NAME${N}  ${D}$VAULT_ROOT${N}"
  if ! is_joined; then
    echo "  This folder is a vault with $(vpy users-count) member(s), but you have not joined it."
    hint "vault join"
    return 0
  fi
  check_onedrive; quarantine_conflicts; check_drift
  local n; n="$(vpy count)"
  echo "  joined as $SELF_USER · $(vpy users-count) members · $n session(s)"
  echo
  if [[ "$n" == "0" ]]; then
    echo "No sessions yet."
    hint "vault new <name>        e.g.  vault new planning-review"
    return 0
  fi
  vpy list --limit=8
  echo
  hint "vault resume <name>     continue one   ·   vault new <name>     start a new one   ·   vault help"
}

cmd_help() {
  cat <<EOF
${B}vault${N} $VAULT_VERSION — shared Claude Code sessions in a shared folder
${D}https://github.com/shashb27/vault${N}

${B}Everyday${N}
  vault                      where am I, what's here, what next
  vault new <name>           start a shared session with a name teammates can find
  vault resume [<name>]      continue a session (waits for sync, checks nobody is in it)
  vault sessions             list sessions: name, state, who started, who last touched
  vault rename <old> <new>   give a session a better name
  vault show <name> [N]      read the last N turns before you jump in

${B}Setup (once)${N}
  vault init [name]          make the current folder a vault, and join it
  vault join                 join the vault in the current folder
  vault doctor               check Claude, OneDrive, join, pins — with fixes
  vault leave [--force]      disconnect this Mac from the vault

${B}When something's off${N}
  vault status [<name>]      health, who's in what, sync state of one session
  vault conflicts [show <#>] sessions two people were in at once; read the lost turns
  vault private              a Claude session here that is NOT shared
  vault update               pull the latest vault
  vault version

${B}Flags${N}  resume --steal (take over someone's session)  ·  resume --now (skip the sync wait)
       new/resume pass any other flags straight to claude

${D}The conversation is shared; the workbench is not — /rewind, background tasks, prompt
history and your personal permissions stay on your own Mac. Trust model: docs/safety.md${N}
EOF
}

# ---------- main ----------

case "${1:-}" in
  "")            cmd_home ;;
  init)          shift; cmd_init "$@" ;;
  join)          shift; cmd_join "$@" ;;
  new|start)     shift; cmd_new "$@" ;;
  resume|open)   shift; cmd_resume "$@" ;;
  sessions|ls|list) shift; cmd_sessions "$@" ;;
  rename)        shift; cmd_rename "$@" ;;
  status)        shift; cmd_status "$@" ;;
  conflicts)     shift; cmd_conflicts "$@" ;;
  show|log)      shift; cmd_show "$@" ;;
  doctor)        shift; cmd_doctor "$@" ;;
  private)       shift; cmd_private "$@" ;;
  leave)         shift; cmd_leave "$@" ;;
  update)        shift; cmd_update "$@" ;;
  version|--version|-v) echo "vault $VAULT_VERSION ($INSTALL_DIR)" ;;
  claude)        shift   # v0.1 compatibility
                 if [[ "${1:-}" == "--private" ]]; then shift; cmd_private "$@"; else cmd_new "$@"; fi ;;
  help|--help|-h) cmd_help ;;
  *) echo "${R}✗${N} unknown command '$1'." >&2; echo >&2; cmd_help >&2; exit 1 ;;
esac
