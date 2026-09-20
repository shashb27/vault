#!/usr/bin/env bash
# Simulated two-user test for vault.
#
# Two "users" (alice, bob) = two folders whose .vault resolves to ONE shared store,
# i.e. two OneDrive replicas with zero sync latency. Runs real `claude -p` calls, so
# Claude Code must be installed and logged in. Takes ~2-4 minutes.
#
#   tests/sim.sh            run everything
#   KEEP=1 tests/sim.sh     keep the temp folders afterwards
set -u
HERE="$(cd "$(dirname "$0")/.." && pwd)"
VAULT="${VAULT_BIN:-$HERE/dist/vault}"   # VAULT_BIN=/path/to/vault to test another implementation (e.g. legacy/vault.sh)
[[ -x "$VAULT" ]] || { echo "no binary at $VAULT — run: go build -o dist/vault ./cmd/vault"; exit 2; }
export VAULT_LEASE_HEARTBEAT_SECS=1
echo "testing: $VAULT ($("$VAULT" version 2>&1))"
BASE="$(mktemp -d "${TMPDIR:-/tmp}/vault-sim.XXXXXX")"
U1="$BASE/vault sim (alice)"          # hostile path: spaces + parens
U2="$BASE/vault sim (bob)"
PASS=0; FAIL=0
pass() { PASS=$((PASS+1)); echo "  ✅ $*"; }
fail() { FAIL=$((FAIL+1)); echo "  ❌ $*"; }
check() { if eval "$1"; then pass "$2"; else fail "$2  [$1]"; fi; }
as_alice() { ( cd "$U1" && VAULT_TEST_USER=alice VAULT_TEST_HOST=alice-mac "$VAULT" "$@" ); }
as_bob()   { ( cd "$U2" && VAULT_TEST_USER=bob   VAULT_TEST_HOST=bob-mac   "$VAULT" "$@" ); }

cleanup() {
  echo; echo "cleanup…"
  as_bob leave --force >/dev/null 2>&1 || true
  as_alice leave --force >/dev/null 2>&1 || true
  if [[ "${KEEP:-0}" != "1" ]]; then rm -rf "$BASE"; fi
  echo "PASS=$PASS FAIL=$FAIL"
  [[ "$FAIL" == "0" ]]
}
trap cleanup EXIT

echo "sim base: $BASE"
mkdir -p "$U1" "$U2"

echo; echo "== 1. alice: vault init (creates + joins, self-test) =="
out="$(as_alice init sim-vault 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]]' "init exit 0"
check '[[ -d "$U1/.vault/sessions" ]]' ".vault/sessions created"
check 'echo "$out" | grep -q "verified: Claude writes into the vault"' "join self-test verified"
check '[[ $(ls "$U1/.vault/sessions"/*.jsonl 2>/dev/null | wc -l) -eq 0 ]]' "self-test transcript removed"
check 'echo "$out" | grep -q "vault new <name>"' "next-step hint after join"

echo; echo "== 2. bob: joins the same store =="
ln -s "$U1/.vault" "$U2/.vault"
out="$(as_bob join 2>&1)"; rc=$?
check '[[ $rc == 0 ]]' "bob join exit 0"
check '[[ $(python3 -c "import json;print(len(json.load(open(\"$U1/.vault/users.json\"))[\"users\"]))") == 2 ]]' "users.json has 2 members"

echo; echo "== 3. alice: vault new falcon (named session, -p) =="
out="$(as_alice new falcon -p "For the whole team: our release train is named FALCON-42. Reply with just OK." 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]]' "new exit 0"
SID="$(ls "$U1/.vault/sessions"/*.jsonl | head -1 | xargs -I{} basename {} .jsonl)"
check '[[ -n "$SID" ]]' "one session file created ($SID)"
check '[[ -f "$U1/.vault/handoff/$SID.done" ]]' "handoff marker written"
check '[[ ! -f "$U1/.vault/leases/$SID.json" ]]' "lease released"
check 'echo "$out" | grep -q "vault resume \"falcon\""' "handoff hint names the session"
ls_out="$(as_alice sessions 2>&1)"
echo "$ls_out" | sed 's/^/    /'
check 'echo "$ls_out" | grep -q "falcon"' "sessions lists NAME falcon"
check 'echo "$ls_out" | grep -q "alice"' "sessions attributes STARTED-BY alice"
check 'echo "$ls_out" | grep -q "handed off"' "state = handed off"

echo; echo "== 4. alice: duplicate name refused =="
out="$(as_alice new falcon -p "x" 2>&1)"; rc=$?
check '[[ $rc != 0 ]]' "second 'falcon' refused (exit $rc)"
check 'echo "$out" | grep -q "already exists"' "explains name exists"
check '[[ $(ls "$U1/.vault/sessions"/*.jsonl | wc -l) -eq 1 ]]' "no new session file"

echo; echo "== 5. bob: resume by NAME, same file, bidirectional =="
out="$(as_bob resume falcon -p "What is our release train named? Reply with just the name." 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]]' "resume exit 0"
check 'echo "$out" | grep -q "FALCON-42"' "answered from alice's context"
check '[[ $(ls "$U1/.vault/sessions"/*.jsonl | wc -l) -eq 1 ]]' "no fork — still one file"
ls_out="$(as_alice sessions 2>&1)"
check 'echo "$ls_out" | grep -E "falcon.*alice.*bob" -q' "LAST-BY is now bob (alice sees it)"

echo; echo "== 6. bob: rename falcon -> kestrel, resume by new name, old name gone =="
out="$(as_bob rename falcon kestrel 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]]' "rename exit 0"
out="$(as_bob resume kestrel -p "What is our release train named? Reply with just the name." 2>&1)"; rc=$?
check '[[ $rc == 0 ]] && echo "$out" | grep -q FALCON-42' "resume by new name works"
out="$(as_bob resume falcon --now -p "x" 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "no session named"' "old name no longer resolves"
out="$(as_bob resume KEST -p "Reply OK." 2>&1)"; rc=$?
check '[[ $rc == 0 ]]' "case-insensitive name prefix resolves"

echo; echo "== 7. torn transcript refused =="
F="$U1/.vault/sessions/$SID.jsonl"; cp "$F" "$BASE/backup.jsonl"
python3 - "$F" <<'PY'
import sys; p=sys.argv[1]; b=open(p,'rb').read(); open(p,'wb').write(b[:-40])
PY
out="$(as_alice resume kestrel --wait 0 -p "x" 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc != 0 ]] && echo "$out" | grep -qi "incomplete"' "torn transcript refused with plain-language reason"
st="$(as_alice status kestrel 2>&1)"
check 'echo "$st" | grep -q "incomplete"' "status shows transcript incomplete"
cp "$BASE/backup.jsonl" "$F"

echo; echo "== 8. lease: carol is in the session =="
mkdir -p "$U1/.vault/leases"
printf '{"user":"carol","host":"carol-mac","pid":1,"session_id":"%s","acquired_at":"%s","ttl_minutes":60}\n' "$SID" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$U1/.vault/leases/$SID.json"
ls_out="$(as_alice sessions 2>&1)"
check 'echo "$ls_out" | grep -q "in use by carol"' "sessions shows 'in use by carol'"
out="$(as_bob resume kestrel --now -p "x" 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "carol@carol-mac is in this session"' "resume refused while carol holds lease"
out="$(as_bob resume kestrel --steal -p "Reply OK." 2>&1)"; rc=$?
check '[[ $rc == 0 ]] && echo "$out" | grep -q "taking over"' "--steal overrides and warns"
check '[[ ! -f "$U1/.vault/leases/$SID.json" ]]' "lease released after steal run"

echo; echo "== 9. no handoff marker, not my session, no tty -> refuse (alice) =="
rm -f "$U1/.vault/handoff/$SID.done"
out="$(as_alice resume kestrel --wait 0 -p "x" 2>&1 </dev/null)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc != 0 ]] && echo "$out" | grep -q "last touched by bob"' "explains who last touched it"
check 'echo "$out" | grep -q "refusing without a terminal"' "refuses non-interactively"
out="$(as_bob resume kestrel --wait 0 -p "Reply OK." 2>&1 </dev/null)"; rc=$?
check '[[ $rc == 0 ]]' "bob (last toucher) may resume his own session without marker"
check '[[ -f "$U1/.vault/handoff/$SID.done" ]]' "marker rewritten after bob exits"

echo; echo "== 10. conflict copy quarantined + readable =="
cp "$F" "$U1/.vault/sessions/$SID-Bob’s MacBook Pro.jsonl"
out="$(as_alice sessions 2>&1)"
check 'echo "$out" | grep -q "two people were in one session"' "quarantine explains what happened"
check '[[ -f "$U1/.vault/conflicts/$SID-Bob’s MacBook Pro.jsonl" ]]' "copy moved to conflicts/"
out="$(as_alice conflicts 2>&1)"
echo "$out" | sed 's/^/    /'
check 'echo "$out" | grep -q "kestrel"' "conflicts list shows session name"
out="$(as_alice conflicts show 1 2>&1)"
check 'echo "$out" | grep -q "FALCON-42"' "conflicts show dumps readable turns"

echo; echo "== 11. json, bare vault, status, doctor =="
out="$(as_alice sessions --json 2>/dev/null)"
check 'echo "$out" | python3 -c "import json,sys; d=json.load(sys.stdin); assert d[0][\"name\"]==\"kestrel\""' "sessions --json is valid and has the name"
out="$(as_alice 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]] && echo "$out" | grep -q "kestrel" && echo "$out" | grep -q "next:"' "bare 'vault' shows sessions + next step"
out="$(as_alice status kestrel 2>&1)"; rc=$?
check '[[ $rc == 0 ]] && echo "$out" | grep -q "safe to continue"' "status <name> exit 0 and says safe"
out="$(as_alice doctor 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 1 ]] && echo "$out" | grep -q "1 problem"' "doctor exit 1 with the conflict as its one problem"

echo; echo "== 12. unnamed bare-claude session shows first prompt; rename by id prefix =="
SID2="$(uuidgen | tr 'A-Z' 'a-z')"
( cd "$U1" && claude -p --session-id "$SID2" "Team fact: the build server is called OSPREY-7. Reply OK." >/dev/null 2>&1 )
out="$(as_alice sessions 2>&1)"
echo "$out" | sed 's/^/    /'
check 'echo "$out" | grep -q "(unnamed) ${SID2:0:8}"' "unnamed session shown with id"
check 'echo "$out" | grep -q "OSPREY-7"' "first prompt shown as fallback"
out="$(as_alice rename "${SID2:0:8}" osprey 2>&1)"; rc=$?
check '[[ $rc == 0 ]]' "rename by 8-char id prefix"
out="$(as_bob resume osprey --wait 0 -p "x" 2>&1 </dev/null)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "no clean-exit marker"' "fresh marker-less bare session still prompts (idle < 15m)"
touch -t "$(date -v-20M +%Y%m%d%H%M)" "$U1/.vault/sessions/$SID2.jsonl"
out="$(as_bob resume osprey --wait 0 -p "What is the build server called? Reply with just the name." 2>&1 </dev/null)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]] && echo "$out" | grep -q "OSPREY-7"' "idle 20m marker-less session resumes by name without prompting"

echo; echo "== 13. v0.1 compat: 'vault claude' maps to new =="
out="$(as_alice claude compat-test -p "Reply OK." 2>&1)"; rc=$?
check '[[ $rc == 0 ]] && as_alice sessions 2>&1 | grep -q compat-test' "'vault claude <name>' works"

echo; echo "== 15. hotfixes: bypass block, settings lockdown, show, members memory, size, heartbeat, memory conflicts =="
out="$(as_alice new bypass-test --dangerously-skip-permissions -p "x" 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "not allowed inside a vault"' "--dangerously-skip-permissions refused"
out="$(as_alice resume kestrel --now --permission-mode bypassPermissions -p "x" 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "not allowed inside a vault"' "--permission-mode bypassPermissions refused"
mkdir -p "$U1/.claude"; printf '{"permissions":{"defaultMode":"bypassPermissions"}}\n' > "$U1/.claude/settings.json"
out="$(as_alice resume kestrel --now -p "x" 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "sets defaultMode to bypassPermissions"' "vault settings with bypassPermissions refuse to launch"
rm -f "$U1/.claude/settings.json"
out="$(as_bob show kestrel 40 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /' | head -12
check '[[ $rc == 0 ]] && echo "$out" | grep -q "FALCON-42" && echo "$out" | grep -q "human>"' "show prints last turns as text"
check '[[ -f "$U1/.vault/sessions/memory/vault-members.md" ]] && grep -q "alice, bob" "$U1/.vault/sessions/memory/vault-members.md"' "members memory file lists alice and bob"
check 'grep -q "vault-members.md" "$U1/.vault/sessions/memory/MEMORY.md"' "MEMORY.md indexes the members file"
out="$(as_alice sessions 2>&1)"
check 'echo "$out" | grep -q "SIZE"' "sessions has a SIZE column"
out="$(VAULT_BIG_SESSION_BYTES=1000 as_alice sessions 2>&1)"
check 'echo "$out" | grep -q "large session"' "large-session warning fires above threshold (env-lowered)"
cp "$U1/.vault/sessions/memory/MEMORY.md" "$U1/.vault/sessions/memory/MEMORY-Bob’s MacBook Pro.md"
out="$(as_alice status 2>&1)"
check 'echo "$out" | grep -q "shared memory has a conflict copy"' "memory conflict copy detected"
rm -f "$U1/.vault/sessions/memory/MEMORY-Bob’s MacBook Pro.md"
( as_alice new heartbeat-test -p "Count slowly from 1 to 5, one number per line, then say DONE." >/dev/null 2>&1 ) &
HBPID=$!; hb=0
for _ in $(seq 1 60); do
  HBSID="$(ls -t "$U1/.vault/leases"/*.json 2>/dev/null | head -1)"
  hb="$( [[ -n "$HBSID" ]] && grep -o '"heartbeats":[0-9]*' "$HBSID" 2>/dev/null | cut -d: -f2 || echo 0 )"
  [[ "${hb:-0}" -ge 1 ]] && break
  kill -0 $HBPID 2>/dev/null || break
  sleep 0.5
done
check '[[ "${hb:-0}" -ge 1 ]]' "lease heartbeat refreshed during a run (heartbeats=$hb)"
wait $HBPID
check '[[ -z "$(ls "$U1/.vault/leases"/*.json 2>/dev/null)" ]]' "lease removed after the run (heartbeat did not resurrect it)"

echo; echo "== 14. bob leaves =="
out="$(as_bob leave 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]]' "leave exit 0"
check '[[ $(python3 -c "import json;print(len(json.load(open(\"$U1/.vault/users.json\"))[\"users\"]))") == 1 ]]' "bob removed from users.json"
check 'echo "$out" | grep -q "does not un-share"' "honest un-share warning"
out="$(as_bob resume kestrel 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "not joined"' "after leave, resume says not joined + hints join"
