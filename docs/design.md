# mdview interactive review — design spec (v2: Go binary + public skill)

**Date:** 2026-06-29
**Status:** Design approved in direction (Go / public repo / skill); pending spec re-review
then implementation plan.
**Supersedes:** the v1 Node/`~/.claude`-personal design. The *behavioral* core is unchanged
and was proven with a live background-wake PoC; v2 changes only language, packaging, and
distribution.

## Problem

A markdown doc rendered to a static `file://` page and opened in a browser has nothing to
talk back to — so a reviewer can't approve / request changes *in the page* and have that
decision reach the waiting Claude Code session. We want the rendered doc to carry
**Approve / Request-changes buttons** whose click delivers the verdict back to the session
immediately, in the same place the content was reviewed.

## Goals

- Rendered doc shows in-page review controls and the tool **waits for a decision** (always-on).
- **Approve** → `{verdict:"approve"}`. **Request changes** → comment box → `{verdict:"changes",comment}`.
- The session gets the verdict the moment the user clicks (no polling, no command-timeout ceiling).
- **Cross-platform** (macOS / Linux / Windows) and **zero runtime dependency** — a single
  static Go binary; nothing for the user to install (no Node, no Python, no npx).
- **Self-contained / offline** — GitHub CSS, mermaid, and the review UI are embedded in the
  binary via `go:embed`; no network at render time.
- **Distributable**: a public GitHub repo builds per-platform binaries via CI; a Claude
  **skill** bootstraps the right binary on first run.
- Never leaves a stuck session or an orphaned server.

## Non-goals

- **Mobile / remote-from-phone review (deferred).** In-page buttons only reach the server on
  the same network; truly-remote is impossible without a relay, and the only secure /
  no-third-party route is Claude Code's first-party Remote Control with a *native*
  `AskUserQuestion` verdict (not in-page buttons). If revisited, that's the route — not a
  tunnel. The binary binds `127.0.0.1` only.
- Inline / per-section PR-style comments — only doc-level Approve / Request-changes.
- Multi-user / remote review. Localhost only.
- A view-only mode flag. Always-on; trivial to add later (YAGNI).

## Decisions recorded

- **Language: Go.** Single static binary; `net/http` for server + SSE; `goldmark` for
  markdown; pure-Go (no cgo) so CI cross-compiles every target with no extra toolchain.
- **Verdict shape:** Approve / Request-changes(+comment); `{verdict, comment?}`.
- **Always-on:** every invocation renders, shows buttons, and blocks for a decision.
- **Distribution:** own public repo **`claude-code-tools/mdview-review`** (under a new GitHub
  **org `claude-code-tools`**) that *doubles as a single-plugin marketplace*; GH Actions
  release pipeline; the Claude skill downloads + checksum-verifies the matching release
  binary on first run. account-switcher stays a separate standalone CLI (different layer —
  a launcher *around* Claude Code, not an in-session plugin); the org just groups them.
- **Tab-close → `dismissed`** (robustness). Binds `127.0.0.1` only. Random ephemeral port.

## Architecture — the `mdview` binary

A single Go program. Flow: read file → render → assemble self-contained HTML (with review
UI injected + token) → serve on a random localhost port → open browser → block until a
verdict (or dismissal) → print `MDVIEW_VERDICT {json}` to stdout → exit 0.

**Embedded assets (`go:embed`):**
- `assets/github-markdown.css` — page styling (inlined into the page `<style>`).
- `assets/mermaid.min.js` — injected + run client-side **only when** the doc contains
  ```` ```mermaid ```` fences (keeps binary-embedded but page-light).
- `assets/review/*` — the review bar markup + client JS (verdict POST + EventSource) + CSS.

**Rendering:** `goldmark` with the GFM extension (tables, strikethrough, task lists,
autolinks) → HTML body. (Minor HTML differences from the old `markdown-it` are acceptable
for review rendering.)

**HTML assembly:** wrap the body in the GitHub-markdown CSS; reserve bottom padding so the
fixed bar never covers content; inject the review UI + client JS with the per-run token
substituted; conditionally inject mermaid + its client render script.

**Server (`net/http`, `127.0.0.1:0`):**
- `GET /` → the assembled, token-injected page.
- `GET /events` → SSE stream; keep open; detect client disconnect via
  `r.Context().Done()`; used for tab-close / no-client detection.
- `POST /verdict` → require `Authorization: Bearer <token>` (or token in the JSON body);
  403 on mismatch; parse `{verdict, comment?}`; validate `verdict ∈ {approve, changes}` and
  that `changes` carries a non-empty `comment`; respond `204`; then finish.
- else → 404.
- Startup: discover the port from the listener, print the live URL to **stderr**
  (`mdview: review server at http://127.0.0.1:<port>/`), open the browser.
- Finish(verdict): guarded so the first outcome wins; print `MDVIEW_VERDICT {json}` to
  **stdout**; exit 0.

**Browser open (cross-platform):** macOS `open <url>`; Windows `cmd /c start "" <url>`;
Linux `xdg-open <url>`. Spawn detached; failure is non-fatal (the no-client backstop covers it).

**Suggested repo Go layout** (small, split by responsibility):
- `main.go` — CLI: args, orchestration, stdout/stderr/exit.
- `internal/render/` — goldmark render + HTML assembly + embedded assets.
- `internal/server/` — http routes, SSE, verdict parsing, lifecycle; the testable core.
- `assets/` — embedded files.
- `*_test.go` co-located with each package.

## Data flow

1. Session runs `mdview <file>` as a **background** command (decision time unbounded by any
   foreground command-timeout).
2. Binary starts, prints the live URL to stderr (session surfaces it as a clickable link),
   opens the browser.
3. User reviews. **Approve** → POST `{verdict:"approve"}`. **Request changes** → comment
   panel → **Submit** → POST `{verdict:"changes", comment}`.
4. Server validates, replies `204`; page shows *"✓ Sent — you can close this tab."*; server
   prints `MDVIEW_VERDICT {…}` and exits 0.
5. Background command completes → session re-invoked → parse the `MDVIEW_VERDICT` line:
   `approve` → proceed; `changes` → read comment, edit, optionally re-render; `dismissed` →
   ask in the terminal how to proceed.

## Output contract

- **stdout** — exactly one sentinel line:
  - `MDVIEW_VERDICT {"verdict":"approve"}`
  - `MDVIEW_VERDICT {"verdict":"changes","comment":"<text>"}`
  - `MDVIEW_VERDICT {"verdict":"dismissed"}`
- **stderr** — the live-URL startup line; diagnostics.
- **exit code** — `0` for every captured outcome; non-zero only for real errors (bad args,
  file missing, listen failure).

## Review UI (in-page)

- **Fixed bottom bar**, GitHub-ish styling: `✅ Approve` (primary) and `✏️ Request changes`.
- **Request changes** expands a panel: labelled `<textarea>` (autofocus), `Submit feedback`
  (disabled until non-whitespace), `Cancel`. ⌘/Ctrl+Enter submits; Esc cancels.
- After a successful POST: replace the bar with *"✓ Sent — you can close this tab."*,
  disable controls, close the EventSource.
- POST failure (server gone): inline "review session ended" notice.
- Mermaid (when present) renders client-side; the reserved bottom padding keeps diagrams
  from being clipped.

## Robustness, port collision & cleanup

- **No port collision, by construction:** listen on `127.0.0.1:0`; the OS assigns a free
  ephemeral port; concurrent runs never collide. The port is read back from the listener.
- **Single-shot:** the process exists only to capture one verdict, then exits.
- **Tab close before deciding:** the page holds an `EventSource('/events')`. On disconnect
  (`r.Context().Done()`), if all clients are gone and no verdict was decided, finish
  `dismissed` (after a short grace to ignore EventSource reconnect blips). `POST /verdict`
  marks decided *before* teardown so a real decision never races into `dismissed`.
- **No-client backstop:** if no SSE client connects within ~60 s (browser never opened),
  finish `dismissed` rather than zombie-waiting.
- **Parent-death watchdog (POSIX):** poll `os.Getppid()`; if it becomes `1` (session died →
  reparented to init), finish `dismissed` and exit. **Windows note:** PPID semantics differ,
  so on Windows orphan cleanup relies on the no-client backstop + max-lifetime + tab-close
  instead.
- **Absolute max-lifetime cap:** a generous ceiling (default ~6 h, env-overridable via
  `MDVIEW_MAX_LIFETIME_SECONDS`) after which the server finishes `dismissed` regardless —
  last-resort backstop, not a decision deadline.
- **Tab on exit:** the server can't close a tab; when it exits the page's EventSource errors
  and the page shows *"review session ended."* so a stale tab is visibly inert.

## Security

- Binds `127.0.0.1` only — never reachable off-host.
- Random ephemeral port; a random per-run token gates `POST /verdict` (403 on mismatch) so
  another local process or stray tab can't inject a verdict.
- Same-origin page → no CORS.
- No network at render time (assets embedded).
- **Binary bootstrap (skill):** downloads only over HTTPS from the pinned GitHub Release and
  **verifies the published SHA-256** before executing; version is pinned in `SKILL.md`.

## Distribution — repo, marketplace & CI

- **GitHub org `claude-code-tools`** (created manually via the web — no `gh org create`).
  It's a lightweight grouping for the user's Claude-related tools; existing repos
  (account-switcher, ghostty-tmux-attach, homebrew-tap) may be transferred in later
  (GitHub auto-redirects old URLs). account-switcher is **not** a plugin — it's a launcher
  *around* Claude Code — so it is **not** in this repo/marketplace; the org just groups them.
- **Repo `claude-code-tools/mdview-review`** (public, MIT, `README.md`) holds *everything*:
  the Go source, embedded assets, CI/release workflows, the plugin definition, and the
  marketplace manifest. The repo **doubles as its own single-plugin marketplace**:
  - `.claude-plugin/marketplace.json` — a marketplace named `claude-code-tools` listing one
    plugin, `mdview-review`, with `source: "./"` (the repo root is the plugin).
  - `.claude-plugin/plugin.json` — the plugin manifest (name, version, description, author).
  - The plugin bundles the **skill** (below) and a **`/mdview` slash command** (manual
    invocation). See "Skill triggering / discoverability".
- **Install (self-serve, no approval):**
  ```
  /plugin marketplace add claude-code-tools/mdview-review
  /plugin install mdview-review
  ```
- **`.github/workflows/release.yml`:** on a `v*` tag, a matrix cross-compiles
  `{darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64}` (pure Go, no cgo →
  `GOOS`/`GOARCH` from one runner), names assets `mdview-<os>-<arch>[.exe]`, writes
  `SHA256SUMS`, and publishes a GitHub Release with those assets.
- **`.github/workflows/ci.yml`:** `go vet` + `go test ./...` + a build of all targets on PR/push.
- **Repo topics:** `claude-code`, `claude-code-plugin`, `markdown-review` (browsable grouping).
- **Later / optional:** an `claude-code-tools/.github` org-profile README indexing the tools;
  a Homebrew formula in the existing tap (manual-CLI channel); a PR to the official
  `claude-plugins-official` directory for reach. All deferred.

## Skill packaging

- The plugin's **skill** lives at `skills/mdview-review/SKILL.md` inside the repo and is
  installed via the plugin (no manual `~/.claude/skills` copy needed once installed through
  the marketplace).
- **First-run bootstrap (agent-orchestrated):** SKILL.md instructs Claude to check the cache
  (`~/.cache/mdview-review/<version>/mdview[.exe]`); if absent, detect OS/arch, download the
  matching pinned release asset from `claude-code-tools/mdview-review` releases, verify its
  SHA-256 against `SHA256SUMS`, mark executable, cache it. No cross-platform installer script
  needed — the agent runs the right command per OS. Version is pinned in `SKILL.md`.
- **Agent workflow** (in SKILL.md): run the binary **backgrounded**; surface the live
  `http://127.0.0.1:<port>/` URL; parse the `MDVIEW_VERDICT` line; act on
  approve / changes / dismissed.
- **Manual CLI:** users may symlink the cached binary onto `PATH` as `mdview` for
  `mdview file.md`; Windows users invoke the cached `mdview.exe` directly.

## Skill triggering / discoverability

Skill auto-selection is description-driven and probabilistic, and our trigger fires on
Claude's *own* intent (wanting the user to approve a doc), not a user request — so we make
triggering reliable in three layers:

- **Precise skill `description` (portable — ships with the plugin).** The only trigger that
  reaches people who install it. Phrase it as a "use whenever you would otherwise…"
  condition, e.g.: *"Use whenever you are about to ask the user to read, review, or approve a
  markdown document — a spec, design doc, or implementation plan. Renders it in the browser
  with in-page Approve / Request-changes buttons and returns the user's verdict. Use this
  instead of telling the user to open a .md file."*
- **CLAUDE.md trigger directive (deterministic — for the author).** The
  `~/.config/.../CLAUDE.md` "Reviewing markdown files" section is **slimmed but kept** (the
  mechanics move into the skill; a one-line trigger remains): *"When you'd have me review a
  markdown file, invoke the `mdview-review` skill; run it backgrounded and act on
  `MDVIEW_VERDICT`."* An always-in-context instruction removes the probabilistic doubt for
  the author's own sessions. (Public installers rely on the description or add their own line.)
- **`/mdview` slash command (manual fallback).** Bundled in the plugin for explicit
  invocation by any user — no guessing required.

## Testing / verification

- **Go unit tests (`httptest`)**: GET / serves injected page incl. token & bar, no
  `__MDVIEW_TOKEN__` left; 404 for unknown; POST without/with wrong token → 403; invalid
  verdict → 400; `changes` without comment → 400; approve / changes → 204 and the decided
  verdict; SSE-disconnect → `dismissed`; no-client + max-lifetime → `dismissed`; the
  orphan-predicate helper.
- **Render test**: goldmark output contains expected elements for a sample doc; mermaid
  injection only when fences present.
- **Concurrency**: two instances → distinct ports, independent resolution.
- **CI**: `go vet`, `go test ./...`, cross-build matrix succeeds.
- **Manual browser checklist**: approve; request-changes+comment; ⌘/Ctrl+Enter & Esc;
  confirmation state; tab-close → dismissed.
- **Skill bootstrap smoke**: download + checksum-verify + run on at least one platform.

## Risks / notes

- Relies on the harness contract that a backgrounded command re-invokes the session on exit
  (PoC-proven).
- goldmark vs markdown-it rendering differs slightly (acceptable for review).
- Embedding `mermaid.min.js` adds ~3 MB to the binary (one-time download cost; acceptable).
- First run downloads the binary (one-time network); thereafter fully offline.
- Windows orphan cleanup leans on backstops rather than the POSIX ppid watchdog.
