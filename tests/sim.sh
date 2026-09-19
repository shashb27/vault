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
VAULT="$HERE/vault.sh"
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
out="$(as_alice new falcon -p "The codeword is FALCON-42. Reply with just OK." 2>&1)"; rc=$?
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
out="$(as_bob resume falcon -p "What is the codeword? Reply with just the codeword." 2>&1)"; rc=$?
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
out="$(as_bob resume kestrel -p "Reply with just the codeword again." 2>&1)"; rc=$?
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
( cd "$U1" && claude -p --session-id "$SID2" "Codeword OSPREY-7. Reply OK." >/dev/null 2>&1 )
out="$(as_alice sessions 2>&1)"
echo "$out" | sed 's/^/    /'
check 'echo "$out" | grep -q "(unnamed) ${SID2:0:8}"' "unnamed session shown with id"
check 'echo "$out" | grep -q "OSPREY-7"' "first prompt shown as fallback"
out="$(as_alice rename "${SID2:0:8}" osprey 2>&1)"; rc=$?
check '[[ $rc == 0 ]]' "rename by 8-char id prefix"
out="$(as_bob resume osprey --wait 0 -p "x" 2>&1 </dev/null)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "no clean-exit marker"' "fresh marker-less bare session still prompts (idle < 15m)"
touch -t "$(date -v-20M +%Y%m%d%H%M)" "$U1/.vault/sessions/$SID2.jsonl"
out="$(as_bob resume osprey --wait 0 -p "Reply with just the codeword." 2>&1 </dev/null)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]] && echo "$out" | grep -q "OSPREY-7"' "idle 20m marker-less session resumes by name without prompting"

echo; echo "== 13. v0.1 compat: 'vault claude' maps to new =="
out="$(as_alice claude compat-test -p "Reply OK." 2>&1)"; rc=$?
check '[[ $rc == 0 ]] && as_alice sessions 2>&1 | grep -q compat-test' "'vault claude <name>' works"

echo; echo "== 14. bob leaves =="
out="$(as_bob leave 2>&1)"; rc=$?
echo "$out" | sed 's/^/    /'
check '[[ $rc == 0 ]]' "leave exit 0"
check '[[ $(python3 -c "import json;print(len(json.load(open(\"$U1/.vault/users.json\"))[\"users\"]))") == 1 ]]' "bob removed from users.json"
check 'echo "$out" | grep -q "does not un-share"' "honest un-share warning"
out="$(as_bob resume kestrel 2>&1)"; rc=$?
check '[[ $rc != 0 ]] && echo "$out" | grep -q "not joined"' "after leave, resume says not joined + hints join"
