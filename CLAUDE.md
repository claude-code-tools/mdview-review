# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`mdview` is a single, self-contained, cross-platform Go binary that renders a markdown file
to a browser page with **Approve / Request-changes** buttons, serves it on localhost, blocks
until the user clicks, then prints one verdict line to stdout and exits. The launching Claude
Code session runs it backgrounded and is woken on exit. It ships as a Claude Code plugin.

## Commands

```bash
go build -o mdview .                       # build the binary
go test ./...                              # all tests
go test -race ./...                        # race detector (run this on server changes)
go test ./internal/server/ -run TestApprove -v   # a single test
go vet ./...

./mdview path/to/file.md                   # render + serve + wait for a verdict
./mdview --print path/to/file.md           # render the self-contained HTML to stdout (no server)

# cross-compile (CI does all 5 targets; pure Go, no cgo):
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/mdview-linux-arm64 .
```

**Embedded-asset gotcha:** the renderer bakes in `internal/render/assets/*`
(`github-markdown.css`, `mermaid.min.js`, `review.{html,css,js}`) via `go:embed`. After
editing any asset you **must rebuild** the binary for the change to take effect — and tests
read the embedded copies too, so a stale build is not involved in `go test`, but a stale
`./mdview` is.

## Architecture

Three pieces, wired in `main.go`: **render → serve → wait**.

- **`internal/render` — `Page(src []byte, token string) (string, error)`**
  goldmark (GFM) renders the markdown body, then the function assembles one self-contained
  HTML string: inlined GitHub CSS, the review UI markup + CSS + client JS, and — only when the
  rendered body contains `language-mermaid` — the embedded mermaid lib + a client script.
  `review.js` contains the literal placeholder `__MDVIEW_TOKEN__`, replaced with the per-run
  token here. mermaid runs at `securityLevel:"strict"` (the page holds the verdict token in JS,
  so untrusted diagram content must not be able to script the page).

- **`internal/server` — `Start(Options) (*Handle, error)` / `Handle.Wait() Verdict`**
  Binds `127.0.0.1:0` (random ephemeral port — collision-free by construction; **never bind a
  fixed port or a non-loopback address**). Routes: `GET /` (the page), `GET /events` (an SSE
  keep-alive used only to detect a closed tab), `POST /verdict` (token-gated). The verdict
  endpoint **flushes the `204` before calling `decide`** — `Wait()` unblocks the instant a
  verdict is decided and `main` may `os.Exit` immediately, so without the flush the browser can
  miss the response and show an error instead of "✓ Sent".
  - **decide-once:** a `sync.Mutex` + `decided` flag guard a single send on a buffered
    (cap-1) `result` channel and a `close(stop)`. Every resolution path (verdict, tab-close,
    backstops) funnels through `decide`; the first wins, the rest are no-ops.
  - **lifecycle goroutine** resolves to `dismissed` on: no SSE client within
    `NoClientTimeout` (browser never opened), `MaxLifetime` reached (hard backstop), or
    `orphaned(os.Getppid())` i.e. ppid became 1 (the launching session died — POSIX only;
    no-op on Windows, where the other two backstops cover cleanup). A closed tab drops the SSE
    connection (`r.Context().Done()`); after a short grace (to ignore reconnect blips) that
    also resolves `dismissed`. There is no path where the server hangs forever.

- **`main.go`** generates the token, renders, starts the server, prints the live URL to
  **stderr**, opens the browser (per-OS), `Wait()`s, then prints `MDVIEW_VERDICT <json>` to
  **stdout** and exits 0.

### The integration contract (do not break casually)

The session that runs `mdview` parses the **last stdout line**:
`MDVIEW_VERDICT {"verdict":"approve"}` · `{"verdict":"changes","comment":"…"}` ·
`{"verdict":"dismissed"}`. The `MDVIEW_VERDICT ` prefix and this JSON shape are the contract
with the plugin's skill (`skills/mdview-review/SKILL.md`) — changing either requires updating
the skill in lockstep. Exit code is **0 for any captured outcome** (including `dismissed`);
non-zero is reserved for real errors. The session runs the binary **backgrounded** and is
woken on its exit (push, not polling).

Verdicts are exactly `approve` | `changes` (requires a non-empty comment) | `dismissed`.
Timeouts are injectable via `Options` (used by tests for fast lifecycle assertions) and
overridable at runtime via `MDVIEW_NO_CLIENT_SECONDS` / `MDVIEW_MAX_LIFETIME_SECONDS`.

**Curated command buttons.** Beneath Approve / Request-changes, the review page shows a
horizontally-scrollable strip of one-click "command" buttons that return a new
`verdict:"command"` outcome (`{command, prompt}`) — the agent executes the returned `prompt`.
The set is the built-in `render.BuiltinCommands()` by default, or a caller-supplied
`MDVIEW_COMMANDS` JSON array (`{id,label,prompt,recommended?}`) that replaces it (`[]` disables).
`Command` lives in `internal/render`; `render.Page` injects the set as JSON into `review.js`,
which builds the pills and POSTs the command verdict through the unchanged `/verdict` path.
`--view` mode has no strip. The SKILL carries a catalog + guidance so agents tailor the set per
document.

## Testing notes

`internal/server` tests start a real server (`Start`) and drive it with a normal `http.Client`
rather than `httptest.Server`, because SSE disconnect detection and real ports are part of what
is under test. Lifecycle tests pass tiny `NoClientTimeout` / `MaxLifetime` / `TabCloseGrace`
durations via `Options`. Run server changes under `-race`.

## Distribution

The repo doubles as its own single-plugin marketplace (`.claude-plugin/marketplace.json` +
`plugin.json`); CI cross-compiles release binaries that the skill downloads + checksum-verifies
on first use. Because all assets are embedded, the released binary has **no runtime
dependencies** and works offline. Full design in `docs/design.md`; implementation plan in
`docs/plan.md`.

**Per-agent sticky tab (live-reload).** With `MDVIEW_KEY` set, the server binds a deterministic
port (`internal/rendezvous.PortForKey`, range 20000–39999) instead of a random one, records a
per-key rendezvous file (`~/.cache/mdview-review/servers/`, overridable via `MDVIEW_STATE_DIR`),
and emits an SSE instance nonce so a reconnecting tab reloads when a new round's server claims
the same port. It stays process-per-round (exits on every verdict), so there is no daemon.
Teardown floors: `--stop` (per key), tab-close/no-client, orphan-reap (`ppid==1`),
`MDVIEW_OWNER_PID` watch, and a 2h max-lifetime. All opt-in: with no `MDVIEW_KEY`, behavior is
unchanged (random port, no rendezvous file).

### Releasing

Don't hand-edit version strings. Run `scripts/release.sh X.Y.Z` — it bumps the version in
both managed spots (`.claude-plugin/plugin.json` and the `vX.Y.Z` binary pin in
`skills/mdview-review/SKILL.md`, 4 occurrences), commits, tags, and pushes. The tag push
triggers `.github/workflows/release.yml`: cross-compile → publish Release + `SHA256SUMS` →
render `.github/mdview.rb.tmpl` with the real checksums → push the formula to
`claude-code-tools/homebrew-tap`. The Homebrew formula is a generated artifact; never edit it
by hand. (`docs/plan.md` mentions `v0.1.0` as a frozen design record — not a release pin.)
