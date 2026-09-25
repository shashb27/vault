# Handing a conversation to a teammate

This is the whole point of a vault, so here it is step by step with what each person sees.
Names below are examples.

## 1. Alex starts a session

```
$ cd ~/Library/CloudStorage/OneDrive-…/TeamVault
$ vault new planning-review
▶ starting shared session planning-review in vault TeamVault
  exit Claude (Ctrl+D or /exit) when you're done — that hands the session to the team.
```

Claude opens as usual, with the session name in the prompt box. Alex works normally.

If Alex forgets the name, `vault new` asks for one. If the name is taken, it says so and
suggests `vault resume` instead.

## 2. Alex exits Claude

`Ctrl+D` or `/exit`. The wrapper asks one line, then hands off:

```
  hand off planning-review — one line for the next person, '@name' to address it (Enter to skip): @sam check the totals in section 3
  uploading planning-review to OneDrive…
✓ session planning-review handed off — teammates can resume it once OneDrive delivers it.
  → next: they run:  vault resume "planning-review"
  → for sam: check the totals in section 3
```

Three things happened: a *clean-exit marker* was written for the session carrying `to`
and `note`, the wrapper waited for OneDrive to finish uploading (up to 45 seconds), and the
note rides the same upload. `@name` is a login or a machine name as `vault members` lists
them; a prefix works when it points at exactly one member. Enter skips the note. In a `-p`
run, or with `VAULT_NO_NOTE=1`, nothing is asked; `vault new … --for sam --note "…"` sets it
up front.

## 3. Sam looks

```
$ cd ~/Library/CloudStorage/OneDrive-…/TeamVault
$ vault
TeamVault  /Users/sam/Library/CloudStorage/OneDrive-…/TeamVault
  joined as sam · 2 members · 1 session(s)

Waiting for you
  planning-review   from alex, 3m ago — "check the totals in section 3"
    → vault resume "planning-review"

NAME              STATE       STARTED-BY  LAST-BY  LAST-ACTIVE    SIZE  ID
planning-review   handed off  alex        alex     3m ago         38K   7b63838e
   for sam: "check the totals in section 3"

  → next: vault resume <name>     continue one   ·   vault new <name>     start a new one
```

The **Waiting for you** block appears only on the machine the note was addressed to (by
login, machine name, or login@machine). Everyone else sees the note under the row.

STATE tells Sam what to expect:

| STATE | Meaning |
|---|---|
| `handed off` | previous person exited cleanly; safe to resume |
| `in use by alex` | Alex has it open right now; wait |
| `syncing` | the transcript on this Mac is incomplete; OneDrive is still delivering it |
| `unknown` | no clean-exit marker — usually a session started with plain `claude` instead of `vault new`, or an exit that hasn't synced yet |

## 4. Sam reads, then resumes

Optional but recommended: `vault show planning-review` prints the last ten turns as
plain text, so Sam knows where Alex stopped before entering.

```
$ vault resume planning-review
  alex left a note for you: "check the totals in section 3"
▶ resuming planning-review — the whole conversation so far is in context.
  exit Claude (Ctrl+D or /exit) when you're done to hand it back.
```

Claude is told the same note, in one descriptive sentence, so Sam can just say "go". The
note is consumed by this resume; Sam's own exit writes a fresh marker.

Sam is now in Alex's conversation. Everything Sam says is appended to the same session,
so Alex will see it too. Claude is told the vault root on Sam's Mac so file paths from
Alex's turns resolve correctly.

**If it isn't ready yet**, `vault resume` waits instead of guessing:

```
  waiting for OneDrive to finish delivering planning-review......
```

It waits up to 90 seconds. If the transcript is still incomplete after that, it stops and
says so; try again in a minute. If the transcript is complete but there's no clean-exit
marker (Alex may still be in it), it asks:

```
! 'planning-review' was last touched by alex and has no clean-exit marker yet.
! either they are still in it, or their exit hasn't synced. If you both type, the session forks.
  continue anyway? [y/N]
```

Say no and ask Alex, unless you're sure. If nobody holds the session and it has been idle
for more than 15 minutes, vault treats it as finished and doesn't ask.

If Alex really is still in it, `vault` shows `in use by alex` and `vault resume` refuses.
`vault resume planning-review --steal` overrides that when you're certain Alex is done
(crashed, forgot to exit).

**If Claude hesitates.** Claude is told, when a session is resumed, that the vault's
members share the whole conversation by agreement and that a change of driver is expected.
In testing, without that note, Claude sometimes declined to repeat to the second person
something the first person had framed as a secret. If it still hesitates, say who you are
and that the vault is shared; that has been enough.

## 5. Sam exits, Alex continues

Same as step 2, other direction. `vault sessions` on Alex's Mac now shows `LAST-BY sam`.

## Leaving a note later

Forgot at exit, or want to redirect a session?

```
$ vault note planning-review @sam "second pass please"
✓ note on planning-review — for sam: "second pass please"
$ vault note planning-review          # read it
$ vault note planning-review --clear
```

`vault note` refuses while someone holds the session, and on a session that has never been
exited cleanly unless it has been idle for over 15 minutes.

## Renaming

Any member can rename a session that nobody is in:

```
$ vault rename planning-review q4-planning
✓ renamed planning-review → q4-planning
```

`/rename` inside Claude works too; vault picks it up.

## Sessions started without `vault new`

Plain `claude` in the vault folder also writes to the shared pool (that's how the join
works). Those sessions show up as `(unnamed) <id>` with their first prompt underneath, and
STATE `unknown` because nothing wrote a clean-exit marker. Give them a name with
`vault rename <id-prefix> <name>`. Prefer `vault new` so teammates get the marker and the
hint.

## What does not travel

`/rewind` checkpoints, background tasks, up-arrow prompt history, and your own permission
grants are per-person. The first time Claude opens in the vault folder it asks you to trust
the folder; that's normal and per-person.
