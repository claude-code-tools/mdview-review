# Per-Agent Live-Reload (Sticky Tab) — Design

**Status:** design / awaiting approval
**Date:** 2026-06-30
**Component:** `mdview` (Go binary) + `skills/mdview-review/SKILL.md`

## Goal

During a review **changes cycle** — review → "request changes" → I edit the doc → review
again — reuse the **same browser tab** instead of opening a fresh tab/URL each round, and
have it re-render the updated document. One persistent tab **per agent** (main session and
each subagent independently), with **no lingering processes**.

## Non-goals

- **No daemon / persistent server.** The process stays one-shot per round ("turns on when a
  review is requested, turns off on the verdict"). This is what keeps pile-up structurally
  impossible and preserves the push-not-poll contract.
- **No `fsnotify` live-on-every-save.** Reload happens at **round boundaries** (when the agent
  re-runs `mdview` after editing), not on every keystroke. A persistent daemon with live-save
  is a documented future upgrade, explicitly out of scope here.

## Background: invariants we must not break

(References: `main.go`, `internal/server/server.go`, `internal/render/render.go`,
`internal/render/assets/review.js`.)

- **Push, not poll.** The binary exits on every verdict; that exit wakes the launching
  session. The integration contract is the last stdout line `MDVIEW_VERDICT {…}`, exit 0 for
  any captured outcome. Live-reload must keep this — so the process still exits per round.
- **Per-run token.** `review.js` carries a per-process token (`__MDVIEW_TOKEN__`) substituted
  at render; `/verdict` is token-gated. Each round's process has its own token.
- **Existing teardown backstops** (`Handle.lifecycle`): no-client (60s), max-lifetime (6h),
  orphan `ppid==1` (POSIX), tab-close (+1s grace). `decide`-once funnels every resolution
  through one buffered send + `close(stop)`.

## Architecture

Process-per-round, but with a **per-agent sticky port** so the same agent's open tab
reconnects and reloads across rounds, plus a **per-key rendezvous file** that makes teardown
definitive and prevents duplicates.

```
Agent round 1:  MDVIEW_KEY=<agent-id> MDVIEW_OWNER_PID=$PPID  mdview file.md
   port = derive(key)  ->  127.0.0.1:<P>   (tab opens; writes rendezvous file)
   user clicks "changes" -> MDVIEW_VERDICT printed -> process EXITS  (I'm woken)
   (the open tab's EventSource keeps retrying /events on :<P>)
Agent edits file.md
Agent round 2:  same MDVIEW_KEY / MDVIEW_OWNER_PID  mdview file.md
   replace-on-reuse: if a stale server for this key is alive, stop it first
   binds :<P> again; the open tab reconnects, sees a NEW instance nonce,
   location.reload() -> fresh render + fresh token
   ... same tab throughout, scoped to THIS agent only
```

### Components

1. **Port derivation** (`internal/server`, new helper)
   - `port = 20000 + (fnv1a(key) % 20000)` → range **20000–39999** (above well-known, below
     the macOS ephemeral range ~49152+, to minimize clashes with OS-assigned ports).
   - **Fallback:** if `Listen` on the derived port fails (taken), fall back to `127.0.0.1:0`
     (random ephemeral, today's behavior) → that round just gets a fresh tab. Self-healing.
   - **No key set** (`MDVIEW_KEY` empty): always `:0` — i.e. today's exact behavior. The
     feature is **opt-in**; CLI/`brew` users and non-SKILL callers are unaffected.

2. **Rendezvous file** (per key)
   - Path: `~/.cache/mdview-review/servers/<fnv1a(key)>.json`, perms **0600**.
   - Contents: `{ pid, port, token, key, startedAt }`.
   - Written after a successful bind; removed on clean exit (best-effort; staleness handled
     below).

3. **Replace-on-reuse (singleton per key)**
   - On startup with a key, read the rendezvous file; if it names a **live** pid, send it
     `SIGTERM` and wait briefly for the port to free, then bind. Guarantees at most **one**
     server per key even after an abnormal previous exit.
   - Stale file (pid dead / port not responding) → ignore and overwrite.

4. **Reload-on-reconnect** (`server.go` `/events` + `review.js`)
   - Server generates an **instance nonce** at start and emits it on the SSE stream
     (`event: hello\ndata: <nonce>\n\n`) right after `: connected`.
   - `review.js` stores the first nonce it sees. On any later `hello` whose nonce **differs**
     (i.e. it reconnected to a *new* server instance on the same port), it calls
     `location.reload()`. A full reload (not a body swap) is required so the page picks up the
     **new per-round token**. Same-nonce reconnects (transient blips) do nothing.

5. **`--stop` flag** (definitive per-key teardown)
   - `MDVIEW_KEY=<id> mdview --stop` reads the rendezvous file, `SIGTERM`s the recorded pid,
     removes the file. **Idempotent** (no server → no-op, exit 0).
   - Lets an agent — especially a subagent — definitively reap its own preview at end-of-task,
     independent of process-tree semantics.

### Teardown stack (defense in depth)

No single point of failure; no path where the server lingers forever.

| Layer | Speed | Fires when | Role |
|---|---|---|---|
| `--stop <key>` | instant | agent asks (end-of-task / trap) | proactive, per-agent |
| tab-close / no-client | ~1s / 60s | user closes tab / never opened | normal "done" |
| orphan-reap `ppid==1` | ~1s | launching shell dies (graceful quit, TaskStop, timeout) | session end (proven) |
| **owner-pid watch** *(new)* | ~1s | the watched session pid dies (incl. **hard crash**) | closes the crash gap |
| max-lifetime | at cap | always, eventually | the absolute floor |

**Owner-pid watch:** new optional env `MDVIEW_OWNER_PID`. When set, `lifecycle` polls
`syscall.Kill(ownerPid, 0)` each tick; on `ESRCH` (gone) → `decide(dismissed)`. The SKILL sets
`MDVIEW_OWNER_PID="$PPID"`, which is the `claude` session pid (the launching shell is a direct
child of it in both main-session and subagent traces). This makes session teardown definitive
**even on a hard `SIGKILL`/OOM crash**, where the launching shell can survive reparented and
`ppid==1` never trips. POSIX-only (Windows no-op, like the existing orphan check). PID-reuse is
not a practical concern at a 1s poll, and max-lifetime floors it regardless.

**Max-lifetime default:** lower from **6h → 2h**. It's the catch-all for the crash-orphan case;
6h is a long time to hold ~25 MB. Still overridable via `MDVIEW_MAX_LIFETIME_SECONDS`.

## Subagents

- A subagent is **not a separate OS process** — its shell runs under the same `claude` pid as
  the main session (verified). So there is no "subagent died → reap" signal; subagent-level
  teardown is handled by **blocking discipline + `--stop`**, while the **owner-pid watch** is
  the session-level floor.
- Per-agent keys mean main + each subagent get **independent** sticky ports/tabs, so concurrent
  reviews don't collide. If two keys collide (rare) or one grabs another's freed port between
  rounds, the loser falls back to a fresh ephemeral tab — no collision, no pile-up.
- SKILL guidance unchanged on run-mode: **main session → background**, **subagent → blocking**
  with a long timeout, and a subagent should `--stop` its key at end-of-task (ideally via a
  shell `trap`).

## Contract & compatibility

- **Push preserved:** process still exits on every verdict. `MDVIEW_VERDICT` shape and exit
  codes unchanged.
- **Backward compatible:** with no `MDVIEW_KEY`, behavior is byte-identical to today
  (ephemeral port, no rendezvous file, no reload-on-reconnect beyond the existing keep-alive).
- **`--view` and `--print` modes:** unchanged.

## Edge cases

- **Derived port taken** → ephemeral fallback (fresh tab that round).
- **Hash collision** between two agents' keys → second falls back to ephemeral.
- **Inter-round "tab steal"** (another process grabs the freed port before round 2) → round 2
  falls back to ephemeral; token still prevents a forged verdict against the foreign server.
- **Stale rendezvous** (pid dead) → overwritten on next start; `--stop` treats as no-op.
- **Windows** → owner-pid watch and orphan-reap are no-ops; relies on no-client / tab-close /
  max-lifetime (consistent with current behavior).

## Security

- Localhost-only bind and token-gated `/verdict` unchanged.
- Rendezvous file is **0600** (it holds the token); stored under the user's cache dir.
- Sticky port is briefly unbound between rounds — acceptable: verdicts remain token-gated, and
  reconnect does a full `GET /` so the tab always adopts the live instance's token.

## Testing

- **Port derivation**: deterministic key → stable port; collision → ephemeral fallback (inject
  a pre-bound listener).
- **Replace-on-reuse**: starting with a key whose rendezvous names a live pid SIGTERMs it then
  binds; verify single survivor.
- **Reload-on-reconnect**: drive `/events`, restart the server on the same port with a new
  nonce, assert the client receives a differing `hello` (unit-level: assert the nonce protocol;
  the `location.reload()` is covered by a small DOM test or manual check).
- **`--stop`**: idempotent (no server → exit 0); with a server → pid reaped, file removed.
- **Owner-pid watch**: set `MDVIEW_OWNER_PID` to a throwaway child pid, kill it, assert mdview
  decides `dismissed` within ~PPIDPoll. (The `ppid==1` orphan path is already verified manually:
  killing the launching shell reparents mdview to launchd and it exits within ~1s.)
- Run server-touching tests under `-race`.

## Open decisions

1. **Max-lifetime default** — 2h proposed (down from 6h). Confirm.
2. **Port range** — 20000–39999 proposed. Confirm or adjust.

## SKILL changes (summary; full wording in the implementation plan)

- Set `MDVIEW_KEY` per agent (main: session id; subagent: its own task id) and
  `MDVIEW_OWNER_PID="$PPID"` on the review invocation.
- Subagent: run blocking and `--stop` the key at end-of-task.
- Document that the review tab now persists per agent across changes rounds.
