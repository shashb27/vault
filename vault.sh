#!/usr/bin/env bash
# vault — Shared Claude Sessions POC (oxmiq/capsule#1559)
#
# A "Vault" is a shared folder (OneDrive for v1) that owns Claude Code session
# state. After a one-time `vault join`, plain `claude` opened at the vault root
# writes its sessions into <vault>/.vault/sessions/, shared with every member.
#
# SECURITY MODEL (read DESIGN.md §7): joining a vault means giving other members
# shell-adjacent trust. Transcripts, project memory, CLAUDE.md and .claude
# settings inside the vault are shared and unauthenticated.
set -euo pipefail

VAULT_DIRNAME=".vault"
LEASE_TTL_MINUTES_DEFAULT=60
UPLOAD_WAIT_SECS=45
SELF_USER="$(whoami)"
SELF_HOST="$(hostname -s)"
STATE_DIR="$HOME/.claude/vault-local-state"   # per-user, never synced
BACKUP_DIR="$HOME/.claude/vault-backups"      # per-user, never synced

# ---------- helpers ----------

die() { echo "vault: error: $*" >&2; exit 1; }
warn() { echo "vault: warning: $*" >&2; }
info() { echo "vault: $*"; }

# Claude Code's project-dir encoding, replicated exactly (verified against v2.1.170):
# each UTF-16 code unit outside [a-zA-Z0-9] -> '-'; if the result exceeds 200 chars,
# truncate to 200 + '-' + base36(abs(java31 hash of the full original path)).
encode_path() {
  python3 - "$1" <<'PY'
import sys
p = sys.argv[1]
b = p.encode('utf-16-le')
units = [int.from_bytes(b[i:i+2], 'little') for i in range(0, len(b), 2)]
enc = ''.join(chr(u) if (48 <= u <= 57 or 65 <= u <= 90 or 97 <= u <= 122) else '-' for u in units)
if len(enc) > 200:
    h = 0
    for u in units:
        h = (h * 31 + u) & 0xFFFFFFFF
    if h >= 1 << 31:
        h -= 1 << 32
    h = abs(h)
    d = "0123456789abcdefghijklmnopqrstuvwxyz"
    s = "0" if h == 0 else ""
    while h:
        s = d[h % 36] + s
        h //= 36
    enc = enc[:200] + "-" + s
sys.stdout.write(enc)
PY
}

# Walk up from cwd to find the vault root (dir containing .vault/)
find_vault_root() {
  local d="$PWD"
  while [[ "$d" != "/" ]]; do
    if [[ -d "$d/$VAULT_DIRNAME/sessions" ]]; then printf '%s' "$d"; return 0; fi
    d="$(dirname "$d")"
  done
  return 1
}

require_vault_root() {
  VAULT_ROOT="$(find_vault_root)" || die "not inside a vault (no $VAULT_DIRNAME/ found here or above). Run 'vault init' first."
  VAULT_ROOT="$(cd "$VAULT_ROOT" && pwd -P)"   # claude encodes the realpath'd cwd
  VAULT_DIR="$VAULT_ROOT/$VAULT_DIRNAME"
  SESSIONS_DIR="$VAULT_DIR/sessions"
  ENC="$(encode_path "$VAULT_ROOT")"
  LINK_PATH="$HOME/.claude/projects/$ENC"
}

json_get() { # json_get <file> <python-expr over obj `d`>
  python3 - "$1" "$2" <<'PY'
import json,sys
try:
    d=json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
try:
    print(eval(sys.argv[2]))
except Exception:
    pass
PY
}

is_cloud_path() { [[ "$1" == "$HOME/Library/CloudStorage/"* ]]; }

provider_eval() { # best-effort File Provider state; undocumented tool, may change
  is_cloud_path "$1" || return 1
  fileproviderctl evaluate "$1" 2>/dev/null | grep -E 'isUploaded|isUploading|isMostRecentVersionDownloaded|isSyncPaused|isKeepDownloaded|isDownloaded' || true
}

uuid_re='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'

now_iso() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }

# ---------- preflight checks (shared by claude/resume/status) ----------

check_symlink() {
  if [[ ! -L "$LINK_PATH" ]]; then
    warn "you have not joined this vault (missing symlink $LINK_PATH). Run 'vault join'."
    return 1
  fi
  local tgt; tgt="$(readlink "$LINK_PATH")"
  if [[ "$tgt" != "$SESSIONS_DIR" ]]; then
    warn "symlink points to '$tgt', expected '$SESSIONS_DIR' — broken join."
    return 1
  fi
  [[ -f "$VAULT_DIR/config.json" ]] || warn "symlink target has no vault marker (config.json) — vault may have moved or been purged."
  return 0
}

check_onedrive() {
  if is_cloud_path "$VAULT_ROOT"; then
    if ! pgrep -x OneDrive >/dev/null 2>&1; then
      warn "OneDrive is NOT running — transcripts and lease will not propagate until it starts."
    fi
    local st; st="$(provider_eval "$VAULT_DIR" || true)"
    if echo "$st" | grep -q 'isSyncPaused = 1'; then
      warn "OneDrive sync is PAUSED — nothing will propagate."
    fi
  fi
}

# OneDrive conflict copies keep the .jsonl suffix but the basename is no longer
# a pure UUID (e.g. '3f2a...-MacBook-Pro.jsonl'). The loser's turns exist ONLY
# in the conflict copy; quarantine so the native /resume picker can't grab it.
quarantine_conflicts() {
  local moved=0 f base
  for f in "$SESSIONS_DIR"/*.jsonl; do
    [[ -e "$f" ]] || continue
    base="$(basename "$f" .jsonl)"
    if [[ ! "$base" =~ $uuid_re ]]; then
      mkdir -p "$VAULT_DIR/conflicts"
      local dest="$VAULT_DIR/conflicts/$(basename "$f")"
      # never overwrite an earlier quarantined copy — it may be the ONLY copy of those turns
      [[ -e "$dest" ]] && dest="$VAULT_DIR/conflicts/$(basename "$f" .jsonl).$(date +%s).jsonl"
      mv "$f" "$dest"
      warn "quarantined OneDrive conflict copy: $(basename "$f") -> .vault/conflicts/ (its turns are NOT in the primary file)"
      moved=1
    fi
  done
  return 0
}

# Warn when shared instruction/permission files changed since this user last ran.
# These files are a deliberate shared surface AND an injection channel (DESIGN §7).
check_drift() {
  mkdir -p "$STATE_DIR"
  local state="$STATE_DIR/$ENC.drift" f h line changed=() first_run=0
  local tracked=("$VAULT_ROOT/CLAUDE.md" "$VAULT_ROOT/.claude/settings.json" "$VAULT_ROOT/.claude/settings.local.json")
  [[ -f "$state" ]] || first_run=1
  local new=""
  for f in "${tracked[@]}"; do
    [[ -f "$f" ]] && h="$(md5 -q "$f" 2>/dev/null || md5sum "$f" | cut -d' ' -f1)" || h="absent"
    new+="$f $h"$'\n'
    if [[ -f "$state" ]]; then
      line="$(grep -F "$f " "$state" 2>/dev/null || true)"
      [[ -n "$line" && "$line" != "$f $h" ]] && changed+=("$f")
    elif [[ "$h" != "absent" ]]; then
      changed+=("$f")   # first run: whatever is already here was never reviewed by YOU
    fi
  done
  printf '%s' "$new" > "$state"
  if [[ "$first_run" == "1" && ${#changed[@]} -gt 0 ]]; then
    warn "FIRST RUN in this vault — these shared instruction/permission files already exist and have never been reviewed by you:"
    for f in "${changed[@]}"; do echo "         - $f" >&2; done
    warn "read them NOW before working here; they steer Claude and grant permissions on your machine."
    return 0
  fi
  if [[ ${#changed[@]} -gt 0 ]]; then
    warn "shared instruction/permission files changed since your last run (another member may have edited them):"
    for f in "${changed[@]}"; do echo "         - $f" >&2; done
    warn "review them before granting permissions. Never use 'don't ask again' inside a vault."
  fi
}

preflight() {
  check_symlink || true
  check_onedrive
  quarantine_conflicts
  check_drift
}

# ---------- lease ----------

lease_file() { printf '%s' "$VAULT_DIR/lease.json"; }

lease_holder_info() { # prints "user host pid acquired_at ttl expired(0/1)" or nothing
  python3 - "$(lease_file)" <<'PY'
import json,sys,datetime
try:
    d=json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
try:
    acq=datetime.datetime.fromisoformat(d["acquired_at"].replace("Z","+00:00"))
    ttl=int(d.get("ttl_minutes",60))
    expired=1 if (datetime.datetime.now(datetime.timezone.utc)-acq).total_seconds()>ttl*60 else 0
    print(d.get("user","?"), d.get("host","?"), d.get("pid",0), d["acquired_at"], ttl, expired)
except Exception:
    print("?","?","0","?","0","1")
PY
}

acquire_lease() { # $1 = session hint, honors $STEAL
  local hint="${1:-new-session}" info_line
  if [[ -f "$(lease_file)" ]]; then
    info_line="$(lease_holder_info)"
    if [[ -n "$info_line" ]]; then
      read -r l_user l_host l_pid l_at l_ttl l_exp <<< "$info_line"
      local held_by_other=0
      if [[ "$l_user" != "$SELF_USER" || "$l_host" != "$SELF_HOST" ]]; then
        held_by_other=1
      elif [[ "$l_pid" != "$$" ]] && kill -0 "$l_pid" 2>/dev/null; then
        held_by_other=1   # another live vault instance of MY OWN on this machine
      fi
      if [[ "$l_exp" == "0" && "$held_by_other" == "1" ]]; then
        if [[ "${STEAL:-0}" == "1" ]]; then
          warn "stealing lease held by $l_user@$l_host (pid $l_pid, acquired $l_at)"
        else
          die "vault is leased by $l_user@$l_host (pid $l_pid) since $l_at (TTL ${l_ttl}m).
       The lease is advisory — it protects against forgetting, not racing —
       but two people appending to one session WILL fork it on OneDrive.
       If you are sure they are done:  re-run with --steal"
        fi
      fi
    fi
  fi
  local tmp="$VAULT_DIR/.lease.$$"
  printf '{"user":"%s","host":"%s","pid":%d,"session_hint":"%s","acquired_at":"%s","ttl_minutes":%d}\n' \
    "$SELF_USER" "$SELF_HOST" "$$" "$hint" "$(now_iso)" "$LEASE_TTL_MINUTES_DEFAULT" > "$tmp"
  mv "$tmp" "$(lease_file)"
  LEASE_ACQUIRED=1
}

release_lease() {
  [[ "${LEASE_ACQUIRED:-0}" == "1" ]] || return 0
  local info_line
  info_line="$(lease_holder_info)"
  if [[ -n "$info_line" ]]; then
    read -r l_user l_host l_pid _ _ _ <<< "$info_line"
    # only release OUR OWN lease — a concurrent instance may have re-acquired it
    if [[ "$l_user" == "$SELF_USER" && "$l_host" == "$SELF_HOST" && "$l_pid" == "$$" ]]; then
      rm -f "$(lease_file)"
    fi
  fi
}

# ---------- handoff ----------

# True when the transcript's most recent entry was written from THIS user's
# vault root (i.e. this run touched it) — synced-in files from other members
# have their cwd in the tail and must NOT get a handoff marker from us.
session_last_cwd_is_mine() {
  python3 - "$1" "$VAULT_ROOT" <<'PY'
import json,sys,os
path, root = sys.argv[1], sys.argv[2]
try:
    with open(path,'rb') as f:
        f.seek(0, os.SEEK_END); size=f.tell()
        f.seek(max(0, size-262144))
        tail=f.read().decode('utf-8','replace').splitlines()
    for ln in reversed(tail):
        try:
            d=json.loads(ln)
        except Exception:
            continue
        if 'cwd' in d:
            sys.exit(0 if d['cwd']==root or d['cwd'].startswith(root+'/') else 1)
    sys.exit(1)
except Exception:
    sys.exit(1)
PY
}

# After claude exits: mark every session THIS RUN touched as cleanly handed
# off, and (best-effort) wait for OneDrive to finish uploading them.
finish_handoff() { # $1 = start marker file
  local start_marker="$1" f uuid waited
  mkdir -p "$VAULT_DIR/handoff"
  while IFS= read -r f; do
    uuid="$(basename "$f" .jsonl)"
    [[ "$uuid" =~ $uuid_re ]] || continue
    # mtime-newer alone is not enough: OneDrive may deliver ANOTHER member's
    # session mid-run; marking it .done would falsely open their handoff gate
    session_last_cwd_is_mine "$f" || continue
    printf '{"user":"%s","host":"%s","released_at":"%s"}\n' "$SELF_USER" "$SELF_HOST" "$(now_iso)" \
      > "$VAULT_DIR/handoff/$uuid.done"
    if is_cloud_path "$f"; then
      waited=0
      while (( waited < UPLOAD_WAIT_SECS )); do
        provider_eval "$f" | grep -q 'isUploading = 1' || break
        sleep 3; waited=$((waited+3))
      done
      info "session $uuid: handoff marker written (upload wait: ${waited}s)"
    else
      info "session $uuid: handoff marker written"
    fi
  done < <(find "$SESSIONS_DIR" -maxdepth 1 -name '*.jsonl' -newer "$start_marker" 2>/dev/null)
}

# ---------- session inspection ----------

# Torn-sync guard: OneDrive can deliver a snapshot cut mid-record.
transcript_tail_valid() {
  python3 - "$1" <<'PY'
import sys
try:
    with open(sys.argv[1],"rb") as f:
        data=f.read()
    lines=[l for l in data.split(b"\n") if l.strip()]
    if not lines: sys.exit(1)
    import json; json.loads(lines[-1])
except Exception:
    sys.exit(1)
PY
}

# ---------- commands ----------

cmd_init() {
  [[ -d "$PWD/$VAULT_DIRNAME" ]] && die "this folder is already a vault."
  mkdir -p "$PWD/$VAULT_DIRNAME"/{sessions,handoff,conflicts}
  mkdir -p "$PWD/$VAULT_DIRNAME/sessions/memory"
  printf '{"vault_id":"%s","name":"%s","created_by":"%s@%s","created_at":"%s","schema":1}\n' \
    "$(uuidgen | tr 'A-Z' 'a-z')" "$(basename "$PWD")" "$SELF_USER" "$SELF_HOST" "$(now_iso)" \
    > "$PWD/$VAULT_DIRNAME/config.json"
  printf '{"users":[]}\n' > "$PWD/$VAULT_DIRNAME/users.json"
  printf '{"sessions":{}}\n' > "$PWD/$VAULT_DIRNAME/index.json"
  cat > "$PWD/$VAULT_DIRNAME/sessions/memory/MEMORY.md" <<'EOF'
# Memory Index

- NOTE: this is SHARED VAULT MEMORY. Everything here is visible to and writable
  by every member of this vault, and is injected into every member's sessions.
  Do not store personal facts here.
EOF
  info "vault initialized at $PWD"
  info "next: each user runs 'vault join' from inside this folder."
  if is_cloud_path "$PWD"; then
    info "OneDrive detected. IMPORTANT (not scriptable on macOS): in Finder, right-click"
    info "'$VAULT_DIRNAME' and choose 'Always Keep on This Device', then verify with 'vault status'."
  fi
}

cmd_join() {
  require_vault_root
  mkdir -p "$HOME/.claude/projects"
  if [[ -L "$LINK_PATH" ]]; then
    local tgt; tgt="$(readlink "$LINK_PATH")"
    [[ "$tgt" == "$SESSIONS_DIR" ]] && { info "already joined (symlink healthy)."; return 0; }
    die "a symlink already exists at $LINK_PATH pointing elsewhere ($tgt). Resolve manually."
  fi
  if [[ -d "$LINK_PATH" ]]; then
    mkdir -p "$BACKUP_DIR"
    local backup="$BACKUP_DIR/$ENC.$(date +%s)"
    mv "$LINK_PATH" "$backup"
    info "existing local sessions for this folder were backed up (NOT shared, NOT merged):"
    info "  $backup"
    info "publishing pre-join sessions into the vault requires explicitly copying them yourself."
  fi
  ln -s "$SESSIONS_DIR" "$LINK_PATH"
  # register user (last-write-wins on users.json is acceptable for the POC)
  python3 - "$VAULT_DIR/users.json" "$SELF_USER" "$SELF_HOST" "$VAULT_ROOT" "$(now_iso)" <<'PY'
import json,sys
p,u,h,vp,ts=sys.argv[1:6]
try: d=json.load(open(p))
except Exception: d={"users":[]}
d.setdefault("users",[])
if not any(x.get("user")==u and x.get("host")==h and x.get("vault_path")==vp for x in d["users"]):
    d["users"].append({"user":u,"host":h,"vault_path":vp,"joined_at":ts})
json.dump(d,open(p,"w"),indent=1)
PY
  # Empirical self-check: claude's path encoding is undocumented and version-
  # specific. If claude resolves this cwd to a DIFFERENT encoded dir than our
  # symlink, sessions would silently NOT be shared — verify before declaring joined.
  # Scoped by a nonce so concurrent claude activity elsewhere can't false-fail
  # the join, and the throwaway transcript is removed so joins don't litter the
  # shared pool.
  local nonce marker found="" stray="" sid
  nonce="$(uuidgen | tr 'A-Z' 'a-z')"
  marker="$STATE_DIR/.join-selftest.$$"
  mkdir -p "$STATE_DIR"; touch "$marker"
  ( cd "$VAULT_ROOT" && claude -p "vault join self-test $nonce — reply OK" >/dev/null 2>&1 ) || true
  local f
  while IFS= read -r f; do
    grep -q "$nonce" "$f" 2>/dev/null && { found="$f"; break; }
  done < <(find "$SESSIONS_DIR" -maxdepth 1 -name '*.jsonl' -newer "$marker" 2>/dev/null)
  if [[ -z "$found" ]]; then
    # transcript not in the vault: either claude couldn't run, or it encoded this
    # cwd differently and wrote elsewhere — look only in dirs touched since the marker
    local d g
    while IFS= read -r d; do
      g="$(grep -l "$nonce" "$d"/*.jsonl 2>/dev/null | head -1 || true)"
      [[ -n "$g" ]] && { stray="$g"; break; }
    done < <(find "$HOME/.claude/projects" -maxdepth 1 -type d -newer "$marker" 2>/dev/null)
  fi
  rm -f "$marker"
  if [[ -n "$found" ]]; then
    sid="$(basename "$found" .jsonl)"
    rm -f "$found" "$VAULT_DIR/handoff/$sid.done"
    rm -rf "$SESSIONS_DIR/$sid"
    info "joined vault '$VAULT_ROOT' as $SELF_USER@$SELF_HOST (self-test: claude used the vault symlink ✓; throwaway transcript removed)"
  elif [[ -n "$stray" ]]; then
    rm -f "$LINK_PATH"
    die "join verification FAILED: claude wrote the self-test transcript to
       $stray
       instead of the vault — its path encoding may have changed in this claude
       version, so sessions would NOT be shared. Symlink rolled back; nothing joined."
  else
    warn "self-test inconclusive: no transcript appeared (claude may be unauthenticated or offline)."
    warn "the symlink is in place but UNPROVEN — run one 'vault claude' session and confirm it appears in 'vault sessions'."
    info "joined vault '$VAULT_ROOT' as $SELF_USER@$SELF_HOST (self-test: inconclusive)"
  fi
  info "plain 'claude' opened at this folder's ROOT now reads/writes SHARED sessions."
  warn "subfolders are NOT shared: launching claude in a subdirectory of the vault creates a private, unshared session (v1 limitation)."
  echo
  echo "  ┌─ TRUST NOTICE (DESIGN.md §7) ─────────────────────────────────────┐"
  echo "  │ Everything Claude prints/reads in vault sessions is shared.      │"
  echo "  │ CLAUDE.md, .claude settings and project memory here are shared   │"
  echo "  │ and editable by every member. Never 'always allow' in a vault.   │"
  echo "  └───────────────────────────────────────────────────────────────────┘"
  if is_cloud_path "$VAULT_ROOT"; then
    echo
    info "OneDrive: right-click '$VAULT_DIRNAME' in Finder → 'Always Keep on This Device'"
    info "(macOS provides no scriptable pin). Verifying current state (best-effort):"
    provider_eval "$VAULT_DIR" | sed 's/^/         /' || true
    # force-materialize what exists today; does not prevent future eviction
    find "$SESSIONS_DIR" -type f -exec cat {} + >/dev/null 2>&1 || true
  fi
}

cmd_claude() {
  require_vault_root
  local private=0 args=()
  STEAL=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --private) private=1; shift ;;
      --steal)   STEAL=1; shift ;;
      *) args+=("$1"); shift ;;
    esac
  done
  if [[ "$private" == "1" ]]; then
    local scratch="$HOME/.claude/vault-private/$(basename "$VAULT_ROOT")"
    mkdir -p "$scratch"
    info "PRIVATE session: launching from $scratch — this session will NOT be shared."
    info "(note: interactive --no-session-persistence does not work; this cwd trick is the real escape hatch)"
    ( cd "$scratch" && exec claude ${args[@]+"${args[@]}"} )
    return $?
  fi
  preflight
  acquire_lease "new-or-picker"
  trap 'release_lease' EXIT INT TERM
  local start_marker="$VAULT_DIR/.run-start.$$"
  touch "$start_marker"
  local rc=0
  ( cd "$VAULT_ROOT" && claude ${args[@]+"${args[@]}"} ) || rc=$?
  finish_handoff "$start_marker"
  rm -f "$start_marker"
  release_lease
  trap - EXIT INT TERM
  return $rc
}

cmd_resume() {
  require_vault_root
  local id="" args=() user_asp=""
  STEAL=0
  # only the FIRST positional may be the session id — a bare UUID later in the
  # arg list belongs to whatever claude flag precedes it
  if [[ $# -gt 0 && "$1" =~ $uuid_re ]]; then id="$1"; shift; fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --steal) STEAL=1; shift ;;
      --append-system-prompt)
        user_asp="${2:-}"; shift 2 ;;   # merged with the reorientation prompt below
      *) args+=("$1"); shift ;;
    esac
  done
  preflight
  if [[ -n "$id" ]]; then
    local f="$SESSIONS_DIR/$id.jsonl"
    [[ -f "$f" ]] || die "session $id not found in this vault (not synced yet? check 'vault status $id')."
    if is_cloud_path "$f"; then cat "$f" >/dev/null 2>&1 || true; fi   # materialize
    if ! transcript_tail_valid "$f"; then
      die "transcript $id ends mid-record — OneDrive is likely still syncing it. Retry shortly."
    fi
    [[ -f "$VAULT_DIR/handoff/$id.done" ]] || warn "no clean-handoff marker for $id — the other user may still be in this session (or exited without the wrapper)."
  fi
  acquire_lease "${id:-picker}"
  trap 'release_lease' EXIT INT TERM
  local start_marker="$VAULT_DIR/.run-start.$$"
  touch "$start_marker"
  local reorient="This session may have moved between machines/users (shared vault). The vault root on THIS machine is: $VAULT_ROOT — re-resolve any absolute file paths from earlier in the conversation relative to this root before using them."
  [[ -n "$user_asp" ]] && reorient="$reorient"$'\n\n'"$user_asp"
  # this session is live again — its old clean-handoff marker is no longer true
  [[ -n "$id" ]] && rm -f "$VAULT_DIR/handoff/$id.done"
  local rc=0
  if [[ -n "$id" ]]; then
    ( cd "$VAULT_ROOT" && claude --resume "$id" --append-system-prompt "$reorient" ${args[@]+"${args[@]}"} ) || rc=$?
  else
    ( cd "$VAULT_ROOT" && claude -r --append-system-prompt "$reorient" ${args[@]+"${args[@]}"} ) || rc=$?
  fi
  finish_handoff "$start_marker"
  rm -f "$start_marker"
  release_lease
  trap - EXIT INT TERM
  return $rc
}

cmd_sessions() {
  require_vault_root
  # index-first: only transcripts that are new or changed since the cached entry
  # are opened (opening a dataless OneDrive file downloads ALL of it); the index
  # is written once at the end.
  python3 - "$SESSIONS_DIR" "$VAULT_DIR/index.json" "$VAULT_DIR/users.json" <<'PY'
import json,sys,os,glob,datetime
sessions_dir, index_path, users_path = sys.argv[1:4]
try: idx = json.load(open(index_path)).get("sessions", {})
except Exception: idx = {}
def user_for(cwd):
    try:
        for u in json.load(open(users_path)).get("users", []):
            if cwd and cwd.startswith(u.get("vault_path", "\0")): return u["user"]
    except Exception: pass
    if cwd and cwd.startswith("/Users/"): return cwd.split("/")[2]
    return "?"
def cwd_in(lines):
    for ln in lines:
        try:
            d = json.loads(ln)
            if "cwd" in d: return d["cwd"]
        except Exception: continue
    return None
rows, new_idx, opened = [], {}, 0
files = sorted(glob.glob(os.path.join(sessions_dir, "*.jsonl")))
for path in files:
    sid = os.path.basename(path)[:-6]
    st = os.stat(path)
    mt = datetime.datetime.fromtimestamp(st.st_mtime).strftime("%Y-%m-%d %H:%M")
    cached = idx.get(sid)
    if cached and cached.get("size") == st.st_size and cached.get("last_active") == mt:
        entry = cached
    else:
        opened += 1
        with open(path, "rb") as f:
            head = []
            for i, ln in enumerate(f):
                head.append(ln.decode("utf-8", "replace"))
                if i >= 40: break
            f.seek(max(0, st.st_size - 262144))
            tail = f.read().decode("utf-8", "replace").splitlines()
        entry = {"owner": user_for(cwd_in(head)),
                 "last_touched_by": user_for(cwd_in(reversed(tail))),
                 "last_active": mt, "size": st.st_size}
    new_idx[sid] = entry
    rows.append((sid, entry["owner"], entry["last_touched_by"], entry["last_active"], entry["size"]))
if not rows:
    print("vault: no sessions in this vault yet.")
else:
    print(f'{"SESSION":<38} {"STARTED-BY*":<14} {"LAST-TOUCH*":<14} {"LAST-ACTIVE":<17} SIZE')
    for r in rows:
        print(f"{r[0]:<38} {r[1]:<14} {r[2]:<14} {r[3]:<17} {r[4]}")
    print(f"\n({opened} transcript(s) opened, {len(rows)-opened} served from index)")
try:
    json.dump({"sessions": new_idx}, open(index_path, "w"), indent=1)
except Exception:
    pass
PY
  echo
  echo "* attribution is derived from transcript contents — a heuristic, NOT a verified identity."
  local n
  n="$(find "$VAULT_DIR/conflicts" -name '*.jsonl' 2>/dev/null | wc -l | tr -d ' ')"
  if [[ "$n" != "0" ]]; then
    warn "$n quarantined conflict cop(ies) in .vault/conflicts/ — turns in them are missing from the primaries."
  fi
}

cmd_status() {
  require_vault_root
  local id="${1:-}"
  echo "vault root : $VAULT_ROOT"
  echo "sessions   : $SESSIONS_DIR"
  if check_symlink; then echo "join       : OK ($LINK_PATH -> sessions)"; else echo "join       : NOT JOINED / BROKEN"; fi
  if [[ -f "$(lease_file)" ]]; then
    local li; li="$(lease_holder_info)"
    if [[ -n "$li" ]]; then
      read -r l_user l_host l_pid l_at l_ttl l_exp <<< "$li"
      echo "lease      : held by $l_user@$l_host (pid $l_pid) since $l_at (TTL ${l_ttl}m)$( [[ "$l_exp" == "1" ]] && echo ' [EXPIRED]' )"
    fi
  else
    echo "lease      : free"
  fi
  local newest
  newest="$(find "$SESSIONS_DIR" -maxdepth 1 -name '*.jsonl' -exec stat -f '%m %N' {} + 2>/dev/null | sort -rn | head -1 || true)"
  if [[ -n "$newest" ]]; then
    echo "last received change: $(date -r "${newest%% *}" '+%Y-%m-%d %H:%M:%S') ($(basename "${newest#* }"))"
    echo "  ^ shows only what has ARRIVED locally — it cannot see un-synced remote changes."
  fi
  if is_cloud_path "$VAULT_ROOT"; then
    if pgrep -x OneDrive >/dev/null 2>&1; then echo "onedrive   : running"; else echo "onedrive   : NOT RUNNING"; fi
    echo "provider state of .vault (best-effort, offline shows false-green):"
    provider_eval "$VAULT_DIR" | sed 's/^/  /' || echo "  (unavailable)"
  else
    echo "onedrive   : n/a (vault is not under ~/Library/CloudStorage)"
  fi
  quarantine_conflicts
  local n; n="$(find "$VAULT_DIR/conflicts" -name '*.jsonl' 2>/dev/null | wc -l | tr -d ' ')"
  echo "conflicts  : $n quarantined"
  # shared memory conflict check (DESIGN §4a): OneDrive conflict copies duplicate
  # an EXISTING stem — 'MEMORY-<Host>.md' or 'MEMORY (1).md' next to 'MEMORY.md'
  local mf stem
  for mf in "$SESSIONS_DIR/memory"/*.md; do
    [[ -e "$mf" ]] || continue
    stem="$(basename "$mf" .md)"
    if [[ "$stem" =~ ^(.+)\ \([0-9]+\)$ || "$stem" =~ ^(.+)-[A-Za-z0-9][A-Za-z0-9\ ]*$ ]]; then
      if [[ -f "$SESSIONS_DIR/memory/${BASH_REMATCH[1]}.md" ]]; then
        warn "possible conflict copy in shared memory/: $(basename "$mf") duplicates ${BASH_REMATCH[1]}.md — two members' Claude instances may have written memory concurrently; reconcile by hand."
      fi
    fi
  done
  check_drift
  if [[ -n "$id" ]]; then
    echo
    echo "session $id:"
    local f="$SESSIONS_DIR/$id.jsonl"
    if [[ ! -f "$f" ]]; then echo "  NOT PRESENT locally (not synced yet, or wrong id)"; return 0; fi
    echo "  present  : yes ($(stat -f '%z bytes, modified %Sm' "$f"))"
    if transcript_tail_valid "$f"; then echo "  tail     : valid JSON (not torn)"; else echo "  tail     : TORN — still syncing, do not resume yet"; fi
    if [[ -f "$VAULT_DIR/handoff/$id.done" ]]; then
      echo "  handoff  : clean ($(cat "$VAULT_DIR/handoff/$id.done"))"
    else
      echo "  handoff  : NO clean-exit marker — other user may still be working"
    fi
    if is_cloud_path "$f"; then
      echo "  provider :"
      provider_eval "$f" | sed 's/^/    /'
    fi
  fi
}

cmd_leave() {
  require_vault_root
  local force=0
  [[ "${1:-}" == "--force" ]] && force=1
  [[ -L "$LINK_PATH" ]] || die "not joined (no symlink at $LINK_PATH)."
  if [[ "$force" != "1" && -f "$(lease_file)" ]]; then
    local li; li="$(lease_holder_info)"
    if [[ -n "$li" ]]; then
      read -r l_user l_host l_pid l_at l_ttl l_exp <<< "$li"
      if [[ "$l_exp" == "0" ]]; then
        die "a session lease is active ($l_user@$l_host since $l_at) — leaving mid-session
       would split its state between the vault and your restored backup.
       Finish/close the session first, or re-run with --force."
      fi
    fi
  fi
  rm "$LINK_PATH"
  local latest
  latest="$(ls -1dt "$BACKUP_DIR/$ENC."* 2>/dev/null | head -1 || true)"
  if [[ -n "$latest" ]]; then
    mv "$latest" "$LINK_PATH"
    info "restored your pre-join local sessions from $latest"
  fi
  # deregister (best-effort)
  python3 - "$VAULT_DIR/users.json" "$SELF_USER" "$SELF_HOST" <<'PY' 2>/dev/null || true
import json,sys
p,u,h=sys.argv[1:4]
d=json.load(open(p))
d["users"]=[x for x in d.get("users",[]) if not (x.get("user")==u and x.get("host")==h)]
json.dump(d,open(p,"w"),indent=1)
PY
  info "left the vault."
  warn "leaving does NOT un-share anything: sessions you ran while joined remain in the"
  warn "vault, on other members' machines, and in OneDrive version history/recycle bin."
}

cmd_help() {
  cat <<'EOF'
vault — Shared Claude Sessions POC (see DESIGN.md; journal: oxmiq/capsule#1559)

  vault init                 make the current folder a vault
  vault join                 one-time per user/machine; after this, plain
                             'claude' in the vault folder uses SHARED sessions
  vault claude [--private] [--steal] [claude args...]
                             lease -> launch claude at vault root -> handoff
  vault resume [<uuid>] [--steal] [claude args...]
                             safe handoff resume (materialize, torn-check,
                             path-reorientation prompt)
  vault sessions             list shared sessions with (unverified) attribution
  vault status [<uuid>]      health, lease, sync state; per-session handoff gate
  vault leave [--force]      remove symlink, restore backup (does NOT un-share);
                             refuses while a lease is active unless --force

The conversation is shared; the workbench is not: checkpoints//rewind,
background tasks, prompt history and per-user settings do not transfer.
EOF
}

# ---------- main ----------

case "${1:-help}" in
  init)     shift; cmd_init "$@" ;;
  join)     shift; cmd_join "$@" ;;
  claude)   shift; cmd_claude "$@" ;;
  resume)   shift; cmd_resume "$@" ;;
  sessions) shift; cmd_sessions "$@" ;;
  status)   shift; cmd_status "$@" ;;
  leave)    shift; cmd_leave "$@" ;;
  help|--help|-h) cmd_help ;;
  *) die "unknown command '${1}'. Try 'vault help'." ;;
esac
