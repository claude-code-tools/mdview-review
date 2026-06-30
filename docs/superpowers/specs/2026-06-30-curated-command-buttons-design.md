# Curated Command Buttons — Design

**Status:** design / awaiting approval
**Date:** 2026-06-30
**Component:** `mdview` (Go binary) + `internal/render/assets/*` + `skills/mdview-review/SKILL.md`

## Goal

Add a row of **curated command buttons** at the bottom of the review page — one-click
shortcuts for the follow-up instructions a user most commonly types ("review with a
subagent", "stress-test this", "simplify", …). Clicking one ends the review round and
returns the chosen instruction to the calling agent, which acts on it. A built-in default
set ships out of the box; a caller can override the set per-invocation.

## Non-goals

- **Not a verdict modifier.** A command button is a *standalone* outcome (a peer of
  Approve / Request-changes), not an add-on to them. "Approve **and** simplify" is out of
  scope (the richer modifier model was considered and deferred).
- **Not a comment flow.** A command button fires immediately on click — no comment box,
  no second step. Free-text feedback remains the job of Request-changes.
- **No server-side command execution.** `mdview` never runs the command; it only reports
  the user's choice back to the agent over the existing exit channel.
- **No commands in `--view` mode.** Overview mode renders no dock at all, so it gets no
  command strip.

## Background: invariants we must not break

(References: `main.go`, `internal/server/server.go`, `internal/render/render.go`,
`internal/render/assets/{review.html,review.css,review.js}`.)

- **Push, not poll.** The binary exits on every resolved review; the last stdout line is
  `MDVIEW_VERDICT {…}` and exit is 0 for any captured outcome. A command click resolves
  exactly like a verdict — process exits, agent is woken.
- **Per-run token.** `/verdict` is gated by a per-process bearer token substituted into
  `review.js` as `__MDVIEW_TOKEN__`. Command clicks POST to the same token-gated endpoint.
- **One outcome per round.** `decide`-once funnels every resolution through a single
  buffered send. A command click goes through the same `send()` / `decide` path; the first
  click wins and the page locks (existing `done` guard).
- **Self-contained page.** The page is assembled from embedded assets; no network beyond
  the localhost server. Command definitions must travel into the page the same way (server
  substitution), not via any external fetch.

## Architecture

The command set is computed in `main.go`, injected into the page as JSON (alongside the
token), rendered into pill buttons by `review.js`, and returned — on click — as a new
`verdict:"command"` outcome through the unchanged `/verdict` → exit → `MDVIEW_VERDICT` path.

```
main.go:  commands = parse(MDVIEW_COMMANDS) or BUILTIN_DEFAULTS   ([]  => none)
   -> render.Page(src, token, commands)
        review.js gets __MDVIEW_TOKEN__ and __MDVIEW_COMMANDS__ (JSON) substituted
   -> browser: review.js builds the scrollable strip from the JSON, wires each button
   user clicks "Stress-test"
   -> POST /verdict  {verdict:"command", command:"devils-advocate", prompt:"…"}  (token-gated)
   -> server decide() -> process exits
   -> stdout: MDVIEW_VERDICT {"verdict":"command","command":"devils-advocate","prompt":"…"}
   agent: verdict==command  => do what `prompt` says (branch on `command` if it wants)
```

### Components

1. **Command model** (a small Go type + the built-in defaults)
   - `type Command struct { ID string `json:"id"`; Label string `json:"label"`; Prompt string `json:"prompt"`; Recommended bool `json:"recommended,omitempty"` }`
   - `Recommended` is an optional flag the *caller* may set on a supplied command to draw the
     user's eye to the follow-up the agent most recommends for this document. Built-in defaults
     never set it. It is presentational only — it changes the pill's style, not the returned
     payload.
   - `BuiltinCommands() []Command` — the curated default 6 (below). Pure data.
   - Lives in `internal/render/commands.go` (with the renderer that consumes it). `main.go`
     already imports `render`, so it references `render.Command` for parsing `MDVIEW_COMMANDS`;
     no new package, no import cycle. The list is editable in one obvious place and reusable
     by tests.

2. **Source resolution** (`main.go`, review mode only)
   - Read `MDVIEW_COMMANDS`. Unset → `BuiltinCommands()`. Present → parse as a JSON array of
     `Command`. Empty array `[]` → no command strip (the off-switch). Invalid JSON or a
     malformed entry (missing/empty `id` or `label`) → print a one-line warning to stderr and
     **fall back to the built-in defaults** (best-effort; never crash review mode).
   - `--view` mode ignores commands entirely.

3. **Injection** (`internal/render/render.go`)
   - `Page(src, token, commands)` JSON-encodes `commands` and substitutes the result for the
     placeholder `__MDVIEW_COMMANDS__` in `review.js` (the same substitution mechanism already
     used for `__MDVIEW_TOKEN__`). JSON encoding handles all escaping of label/prompt text —
     no manual HTML escaping, no injection surface in the prompt strings.
   - `View(src)` is unchanged (no dock, no commands). **Signature change:** `Page` gains a
     `commands []Command` parameter; the single caller (`main.go`) is updated.

4. **Strip rendering + scroll fades** (`internal/render/assets/review.{html,css,js}`)
   - `review.html` gains an empty `<div id="mdview-commands" role="group"
     aria-label="Quick commands">` as the **first child of `#mdview-bar`**, above
     `#mdview-row`.
   - `review.js` parses `__MDVIEW_COMMANDS__`; for each entry it appends a
     `<button class="mv-cmd" type="button">label</button>` carrying its `id`/`prompt` (held in
     JS, not the DOM, to avoid escaping concerns), and wires `click → send({verdict:"command",
     command:id, prompt})`. If the list is empty, the strip stays empty and is hidden
     (collapses to zero height — no divider, no gap).
   - **Recommended highlight.** Entries with `recommended:true` get an extra
     `mv-cmd--recommended` class → a "featured" style: a gold **sparkle** mark (a large + small
     four-point shine, the conventional highlights glyph) plus a violet→indigo gradient border,
     so the agent's suggested follow-up stands out without reusing the green Approve color
     (which would read as "already approved"). Purely visual; the click payload is identical to
     any other command. The SKILL instructs agents to mark at most one or two — highlighting
     everything highlights nothing.
   - **Scroll fades.** The strip is `overflow-x:auto` with the scrollbar hidden. A scroll +
     resize listener toggles state classes on the strip based on position:
     - not scrolled (`scrollLeft≈0`) and overflowing → right fade only
     - scrolled and more remains → both fades
     - at the end (`scrollLeft ≈ scrollWidth - clientWidth`) → left fade only
     - no overflow → no fade
     CSS implements the fade with `mask-image: linear-gradient(...)`, switching the gradient
     per state class (`is-start` / `is-end`; both absent = both edges fade). Behind
     `prefers-reduced-motion` nothing animates; the mask is static styling, so it stays.
   - **Hierarchy & states.** `.mv-cmd` is a secondary pill (smaller, ghost-like, visually
     subordinate to the green Approve). When the changes panel opens (`panel-open`), the strip
     hides alongside `#mdview-row` (existing pattern). In the done state (`mv-done`), the whole
     bar is replaced by the confirmation, so the strip is gone.

5. **Verdict acceptance** (`internal/server/server.go`)
   - `/verdict` accepts `verdict:"command"` with a non-empty `command` string and an optional
     `prompt` string. Empty/missing `command` → 400 (mirrors how `changes` requires a
     comment). The `Verdict` struct gains `Command string `json:"command,omitempty"`` and
     `Prompt string `json:"prompt,omitempty"``. Resolution goes through the existing
     `decide`; the emitted `MDVIEW_VERDICT` JSON carries the new fields. Token gate and
     localhost bind unchanged — that is the security boundary; `command`/`prompt` are
     user-chosen via the page the server itself rendered, so any non-empty command is accepted.

### The built-in default set (6)

| label | id | prompt |
|---|---|---|
| Review with subagent | `review-with-subagent` | Dispatch an independent subagent to review this document for gaps, risks, and weak assumptions, and report its findings before proceeding. |
| Stress-test (devil's advocate) | `devils-advocate` | Challenge this document: surface edge cases, failure modes, and unstated assumptions that could break it, before I proceed. |
| Simplify / tighten | `simplify` | Simplify and tighten this — cut unnecessary scope and complexity without losing essential content — then re-render for review. |
| Add more detail | `add-detail` | Expand the thin or vague parts of this with concrete specifics, then re-render for review. |
| Verify against codebase | `verify-against-codebase` | Verify this against the actual codebase — confirm the files, APIs, types, and assumptions it references exist and are accurate, and flag any drift before I proceed. |
| Explore alternatives | `explore-alternatives` | Lay out 2–3 alternative approaches to what this proposes, with tradeoffs and a recommendation, before I commit to this one. |

(Held back from the built-in 6 but available in the SKILL catalog: `decompose`. Deliberately
excluded entirely: `explain` — it only narrates the doc back to the user rather than
*strengthening* it, and loops straight back to needing another decision, so it doesn't fit the
buttons' purpose.)

## Override contract: `MDVIEW_COMMANDS`

- Format: a JSON array of `{ "id": string, "label": string, "prompt": string,
  "recommended"?: bool }`.
- Semantics: **replace** the built-in set (not merge). Unset → defaults; `[]` → none;
  non-empty → exactly the supplied buttons, in order.
- Validation: each entry needs a non-empty `id` and `label`; `prompt` may be empty (a button
  with no prompt returns `command` with an empty `prompt` — the agent branches on `id`);
  `recommended` is optional and defaults to `false`.
- Failure handling: malformed JSON / invalid entries → stderr warning + built-in defaults.
- Rationale: replace is predictable and yields both the off-switch (`[]`) and full
  customization without a merge/dedup story. (A future `MDVIEW_COMMANDS_EXTEND` could add
  merge semantics; not needed now.)

## Data flow recap

env `MDVIEW_COMMANDS` → `main.go` (parse/validate/fallback) → `render.Page(src, token,
commands)` (JSON-substitute `__MDVIEW_COMMANDS__`) → `review.js` (build strip + fades + wire
clicks) → user click → `POST /verdict {verdict:"command",command,prompt}` (token-gated) →
`decide` → process exit → `MDVIEW_VERDICT {…}` → agent executes `prompt`.

## Error handling

- **Bad `MDVIEW_COMMANDS`** → warn on stderr, use defaults. Never crash; never show a broken
  strip.
- **Empty list** → no strip; Approve/Request-changes unaffected.
- **Server rejects empty `command`** (400) → `review.js` shows the existing "couldn't record"
  message (same path as any non-OK `/verdict` response).
- **A command click after another outcome** is impossible (the `done` guard + `decide`-once).

## Security

- No new endpoints; `/verdict` stays the only mutation, token-gated, localhost-only.
- Command/prompt strings are user-selected from the server's own rendered buttons; the token
  gate prevents foreign POSTs. Accepting any non-empty `command` is therefore safe.
- Prompt/label text is injected as JSON (not HTML), so it cannot break out into markup; the
  buttons set `textContent`, never `innerHTML`, for labels.

## Edge cases

- **Long label list** → horizontal scroll with dynamic both-edge fades (above).
- **One/zero commands** → no fade (no overflow); zero → strip hidden.
- **Very long prompt** → fine; it travels as JSON and is echoed in `MDVIEW_VERDICT`.
- **`--view`** → no strip, no `__MDVIEW_COMMANDS__` consumption.
- **Reduced motion** → fades are static masks (no animation); buttons keep transitions off
  under the existing `prefers-reduced-motion` rule.
- **Narrow viewport** (existing ≤520px rule hides the label) → the strip still scrolls; pills
  remain reachable.

## Contract & compatibility

- **Backward compatible.** With `MDVIEW_COMMANDS` unset, callers that never heard of this
  feature get the built-in strip; the existing approve/changes/dismissed outcomes are
  unchanged in shape. Agents on the old SKILL that don't recognize `verdict:"command"` simply
  won't see one unless a user clicks a command button — and the new SKILL teaches them to.
- **`render.Page` signature changes** (adds `commands`). Internal API; the one caller is
  updated in the same change. (No external consumers — `Page`/`View` are package-internal.)
- **Builds for windows/amd64** unchanged (pure Go + assets; no platform primitives added).

## Testing

- **render** (`render_test.go`): default `Page` output contains the 6 default labels and a
  `mv-cmd` button scaffold; `__MDVIEW_COMMANDS__` is replaced by valid JSON (no leftover
  placeholder); custom `commands` slice renders custom labels; a `recommended:true` entry's
  JSON carries the flag (and the JS wires the `mv-cmd--recommended` class — asserted via the
  page substring); empty slice → no `mv-cmd` markup; `View` output has no command strip.
- **commands**: `BuiltinCommands()` returns 6 with unique non-empty ids/labels.
- **main** (`main_test.go`): `MDVIEW_COMMANDS` parse — valid JSON → those commands; `[]` →
  none; bad JSON → defaults (+ stderr warning); and an integration round-trip where a posted
  `{verdict:"command",command:"x",prompt:"y"}` exits 0 and the `MDVIEW_VERDICT` line carries
  `command` and `prompt`.
- **server** (`server_test.go`): `/verdict` accepts a `command` verdict (204) and the waited
  `Verdict` carries `command`/`prompt`; empty `command` → 400; token gate still enforced.
- **review.js wiring** (asserted via rendered page): the page wires a click handler that POSTs
  `verdict:"command"` and a scroll listener toggling the fade state classes (assert the
  substrings exist, consistent with the existing JS-wiring smoke checks; JS control flow can't
  run in a Go test).
- Server-touching tests run under `-race`.

## SKILL / docs changes (summary; full wording in the implementation plan)

The SKILL gets three additions: how to **act on** a command verdict; a clear statement of the
buttons' **purpose**; and a **catalog** of well-described candidate commands the agent reads,
selects from, and extends — so it actively suggests good, document-specific commands instead of
always defaulting to the built-in 6.

- **Act on the verdict** — add a `verdict:"command"` case to Step 4: *do what `prompt` says*
  (branch on `command` only if you want programmatic dispatch). Treat it as a user-directed
  instruction about this document.

- **Purpose (stated up front).** The command buttons exist for one job: **strengthen the
  document before the user commits to it.** Every button offered must be a follow-up that makes
  the plan/doc better, safer, or more complete — never a no-op or vanity action.

- **Choose dynamically, from the document and the workload.** There is **no fixed
  use-case→button mapping** — the right set varies enormously, and the agent decides. Reason
  about what would most strengthen *this* doc: a chat feature invites researching established
  realtime patterns; an auth / payments / nginx doc invites a security pass; an
  interaction-heavy flow invites a user-friction review; a code-change plan invites
  footgun / DRY / silent-failure lenses. These are *illustrations of the reasoning*, not rules —
  the right set for a given review may look nothing like them. Default (unset `MDVIEW_COMMANDS`)
  gives the generic 6; tailor whenever you can do better.

- **A catalog to pick from (read the descriptions).** The SKILL documents a menu of
  well-described candidate commands — each with a label, a ready-to-use `prompt`, and a
  *"reach for it when…"* note — so the agent selects relevant ones rather than inventing every
  time. It composes `MDVIEW_COMMANDS` from {built-ins it keeps} + {catalog entries it picks,
  prompts adapted to the doc} + {custom ones it writes}. The catalog is a **starter set, not
  closed**:

  | label | id | reach for it when… |
  |---|---|---|
  | Review with subagent | `review-with-subagent` | almost any substantive plan/spec — independent eyes before committing |
  | Stress-test (devil's advocate) | `devils-advocate` | risky assumptions, complex logic, high-stakes or irreversible changes |
  | Verify against codebase | `verify-against-codebase` | the doc makes concrete claims about files/APIs/types/schema (catch drift) |
  | Security review | `security-review` | auth, payments, infra/proxy, secrets/PII, or anything handling untrusted input |
  | Silent-failure / error gaps | `silent-failure-review` | async/optimistic flows, network/IO, retries, partial-failure paths |
  | Research established patterns | `research-patterns` | widely-solved problems (chat, auth, pagination, public APIs) with strong prior art |
  | Explore alternatives | `explore-alternatives` | a design that commits to one approach without weighing others |
  | Review user friction | `user-friction-review` | multi-step / interaction-heavy flows, onboarding, forms (drop-off risk) |
  | Performance / scalability | `performance-review` | hot paths, fan-out, large-N data, real-time / high-concurrency |
  | Footguns / DRY / smells | `code-quality-review` | code-change plans where maintainability and duplication matter |
  | Test coverage plan | `test-coverage` | a plan that doesn't specify how it'll be tested |
  | Simplify / cut scope | `simplify` | over-large or over-engineered plans |
  | Add more detail | `add-detail` | thin or vague sections |
  | Decompose / split | `decompose` | a large multi-part plan better built as several |

- **Invent custom buttons when warranted.** The catalog is a floor, not a ceiling. The
  5-document validation (below) produced excellent doc-specific buttons the catalog doesn't
  name — *Pre-hijacking coverage* (auth plan referencing specific defenses), *WebSocket proxy
  gaps* and *Rollback / runbook* (nginx), *Autosave failure modes* (resumable wizard),
  *Compare to Stripe / Adyen* (payments API). Write these freely when the document calls for it.

- **Write a good command:** `id` (kebab-case, stable), `label` (≤ ~3 words), `prompt` (a
  complete imperative instruction the agent can execute verbatim, typically ending with "before
  I proceed" or "then re-render for review").

- **Highlight sparingly:** set `recommended:true` on **at most one or two** — the follow-up
  you'd most advise for *this* doc. Highlighting everything highlights nothing.

- **Keep it small** (≤ ~6) so the strip stays curated, and **never surprise the user**: buttons
  are suggestions, not auto-actions — nothing runs until the user clicks.

- **Worked example** (large implementation plan):
  ```bash
  MDVIEW_COMMANDS='[
    {"id":"decompose","label":"Decompose","prompt":"Break this plan into smaller independent plans that can each be built and reviewed on their own, then re-render.","recommended":true},
    {"id":"verify-against-codebase","label":"Verify vs codebase","prompt":"Verify every file/API/type this plan references actually exists and matches, and flag drift before I proceed."},
    {"id":"security-review","label":"Security review","prompt":"Review this plan for security gaps relevant to what it builds, and report findings by severity before I proceed."}
  ]' MDVIEW_KEY="$KEY" MDVIEW_OWNER_PID="$PPID" $BIN plan.md
  ```

- **CLAUDE.md / README.md** — note the curated command buttons, the `MDVIEW_COMMANDS` override
  (JSON array; replace-semantics; `[]` disables; optional `recommended`), and the built-in 6.
- **Version pins** (`VER=` in SKILL.md) are bumped by `scripts/release.sh`, not hand-edited.

## Validation (agent-suggestion test)

Before locking the design, five subagents were each given the *purpose* + the draft catalog +
one representative document, and asked what command set they'd show:

| document | what the agent proposed (highlights) | recommended |
|---|---|---|
| Real-time chat plan | verify-existing-infra, security, stress-test-at-scale, silent-failure, simplify, DB-schema review | infra + security |
| nginx reverse-proxy doc | verify-vs-codebase, security, rollback/failure-modes, WebSocket-proxy-gaps, simplify, runbook | verify + security |
| Auth rework plan | security-audit, verify-vs-codebase, devil's-advocate, **pre-hijacking coverage**, task-sizing, e2e-test-plan | security + verify |
| Onboarding wizard doc | stress-test-branching, **autosave failure modes**, drop-off/user-friction, verify, security, simplify | branching + autosave |
| Public payments API spec | security, verify-vs-codebase, silent-failure, dev-experience, contract-completeness, **compare-to-Stripe/Adyen** | security + verify |

**Findings that shaped the design:**
- The dynamic approach produces sensible, *varied*, on-target sets — no two scenarios matched.
- **Security review** appeared in all 5 (recommended in all 5); **verify-against-codebase** in
  4/5; **silent-failure** and **stress-test** recurred. → these belong in the catalog
  prominently (verify is already a built-in default).
- Agents readily **invented** strong doc-specific buttons (bold above) → the catalog must be a
  starter set with explicit "invent custom" guidance, not a closed list.
- No agent over-stuffed the bar; all stayed at ~6 → the "keep it small" guidance matches
  natural behavior.

## Open decisions

- **Recommended highlight (added per earlier review feedback)** — included as a minimal optional
  `recommended` flag + a distinct pill style, driven by the SKILL's "highlight sparingly"
  guidance. Flagged for confirmation: keep it in v1, or drop the visual highlight and keep
  only the SKILL curation guidance? (Recommendation: keep — it's cheap and makes the agent's
  suggestion legible.)

Otherwise settled — protocol (A / new verdict type), source (C1 / built-in + override),
payload ((2) id+label+prompt+optional recommended), the default 6, the SKILL catalog +
dynamic-selection guidance (validated above), replace semantics, and the dynamic both-edge
scroll fade. Remaining choices (exact pill sizing, fade width, accent color) are
implementation-level and live in the plan.
