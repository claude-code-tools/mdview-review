---
name: mdview-review
description: Use whenever you are about to ask the user to read, review, or approve a markdown document — a spec, design doc, or implementation plan. Renders it in the browser with in-page Approve / Request-changes buttons and returns the user's verdict (and any comment) to the session. Use this instead of telling the user to open a .md file.
---

# mdview-review

Render a markdown file in the browser with **Approve / Request-changes** buttons and get the
user's decision back in one step — instead of telling them to open a file and type a reply.

## When to use

Whenever you would otherwise ask the user to open, read, or approve a `.md` file (a spec,
plan, or design doc), use this skill. Run it, surface the URL, and act on the verdict.

## Step 1 — ensure the binary is cached (first run only)

The tool is a single self-contained binary published on GitHub Releases. On macOS/Linux,
download + checksum-verify the pinned version into a cache (idempotent — skips if present):

```bash
VER=v0.2.0
DIR="$HOME/.cache/mdview-review/$VER"
os=$(uname -s | tr '[:upper:]' '[:lower:]'); case "$os" in linux) os=linux;; darwin) os=darwin;; *) os=windows;; esac
arch=$(uname -m); case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; esac
asset="mdview-$os-$arch"; BIN="$DIR/mdview"
if [ ! -x "$BIN" ]; then
  mkdir -p "$DIR"
  base="https://github.com/claude-code-tools/mdview-review/releases/download/$VER"
  curl -fsSL "$base/$asset" -o "$BIN"
  curl -fsSL "$base/SHA256SUMS" -o "$DIR/SHA256SUMS"
  want=$(grep " $asset$" "$DIR/SHA256SUMS" | awk '{print $1}')
  got=$(shasum -a 256 "$BIN" | awk '{print $1}')
  [ -n "$want" ] && [ "$want" = "$got" ] || { echo "mdview: checksum verification failed"; rm -f "$BIN"; exit 1; }
  chmod +x "$BIN"
fi
echo "$BIN"
```

On **Windows**, download `mdview-windows-amd64.exe` from the same release into
`%USERPROFILE%\.cache\mdview-review\v0.2.0\mdview.exe` and use that path.

## Step 2 — run it and wait (review mode — the default)

Run the cached binary on the file. Set two env vars so your review reuses **one tab across
rounds** and tears down cleanly:

- `MDVIEW_KEY` — a stable id unique to **you** (main session: your session id; a subagent: its
  own task id). The same key reuses the same browser tab across review rounds.
- `MDVIEW_OWNER_PID="$PPID"` — lets the server exit if this session dies.

```bash
MDVIEW_KEY="<your-stable-id>" MDVIEW_OWNER_PID="$PPID" \
  $HOME/.cache/mdview-review/v0.2.0/mdview <path-to-file.md>
```

It blocks until the user clicks a button. **How you run it depends on who you are:**

- **Main session:** run it as a **background** command (so the wait isn't bounded by a command
  timeout). You're re-invoked the moment the user clicks — push, not polling. Do **not** poll it.
- **Subagent:** you run once to completion and can't be re-invoked, so run it **foreground /
  blocking** with a long timeout (up to the 10-minute max). The user should click within that.
  At end-of-task, definitively tear down your preview with:

  ```bash
  MDVIEW_KEY="<your-stable-id>" $HOME/.cache/mdview-review/v0.2.0/mdview --stop
  ```

  Use a shell `trap` to ensure teardown even on early exit:

  ```bash
  trap 'MDVIEW_KEY="<your-stable-id>" $HOME/.cache/mdview-review/v0.2.0/mdview --stop' EXIT
  ```

## Step 3 — surface the URL

It prints `mdview: review server at http://127.0.0.1:PORT/` to stderr. Surface that
`http://127.0.0.1:PORT/` link to the user so they can reopen the page if they lose the tab.

## Step 4 — act on the verdict

When the command exits, parse the last `MDVIEW_VERDICT` line on stdout:

- `MDVIEW_VERDICT {"verdict":"approve"}` → proceed.
- `MDVIEW_VERDICT {"verdict":"changes","comment":"…"}` → read the comment, make the requested
  changes, and (if useful) re-render the updated doc for another review round.
- `MDVIEW_VERDICT {"verdict":"dismissed"}` → the user closed the tab or didn't decide; ask how
  they'd like to proceed.
- `MDVIEW_VERDICT {"verdict":"command","command":"…","prompt":"…"}` → the user clicked a curated
  command button. **Do what `prompt` says** (it's a complete instruction about this document);
  branch on `command` only if you want programmatic dispatch. After acting, re-render the
  updated doc for another review round when appropriate.

## Curated command buttons (suggesting good follow-ups)

The review page shows a row of **command buttons** beneath Approve / Request-changes. Their one
purpose: **strengthen the document before the user commits to it.** Every button you offer must
be a follow-up that makes the plan/doc better, safer, or more complete.

**Choose dynamically, from the document and the workload.** There is no fixed use-case→button
mapping — reason about what would most strengthen *this* doc. With `MDVIEW_COMMANDS` unset the
user gets a generic built-in set; supply your own whenever you can do better. Set it to a JSON
array of `{ "id", "label", "prompt", "recommended"? }` — this **replaces** the defaults (`[]`
disables the strip entirely):

    MDVIEW_COMMANDS='[{"id":"...","label":"...","prompt":"...","recommended":true}]' \
      MDVIEW_KEY="<id>" MDVIEW_OWNER_PID="$PPID" \
      $HOME/.cache/mdview-review/v0.2.0/mdview <file.md>

**A catalog to draw from** (a starter set, not a closed list — adapt prompts to the doc, and
invent doc-specific buttons freely):

| label | id | reach for it when… |
|---|---|---|
| Review with subagent | `review-with-subagent` | almost any substantive plan/spec — independent eyes |
| Stress-test | `devils-advocate` | risky assumptions, complex logic, high-stakes/irreversible changes |
| Verify vs codebase | `verify-against-codebase` | the doc makes concrete claims about files/APIs/types/schema |
| Security review | `security-review` | auth, payments, infra/proxy, secrets/PII, untrusted input |
| Failure gaps | `silent-failure-review` | async/optimistic flows, network/IO, retries, partial-failure paths |
| Research patterns | `research-patterns` | widely-solved problems (chat, auth, pagination, public APIs) |
| Explore alternatives | `explore-alternatives` | a design committing to one approach without weighing others |
| User friction | `user-friction-review` | multi-step / interaction-heavy flows, onboarding, forms |
| Performance | `performance-review` | hot paths, fan-out, large-N data, real-time/high-concurrency |
| Footguns / DRY | `code-quality-review` | code-change plans where maintainability/duplication matter |
| Test coverage | `test-coverage` | a plan that doesn't specify how it'll be tested |
| Simplify | `simplify` | over-large or over-engineered plans |
| Add detail | `add-detail` | thin or vague sections |
| Decompose | `decompose` | a large multi-part plan better built as several |

**Rules of thumb:** `label` ≤ ~3 words; `prompt` a complete imperative instruction you can
execute verbatim (usually ending "before I proceed" or "then re-render for review"); keep the
set small (≤ ~6) so it stays curated; mark **at most one or two** `recommended: true` (the
follow-up you'd most advise — highlighting everything highlights nothing). The buttons are
suggestions, never auto-actions: nothing runs until the user clicks.

## Overview mode — when no decision is needed

Most of the time you want the review flow above. But if you only want to **show** the user a
rendered doc with **no decision required** (an overview / FYI), add `--view`:

```
$HOME/.cache/mdview-review/v0.2.0/mdview --view <path-to-file.md>
```

It renders the doc **without** the buttons, opens it in the browser, and **returns
immediately** (no server, no waiting, no verdict). Use this only when you need no feedback;
otherwise default to the review flow.

## Browser override

`mdview` opens the OS default browser. To force a specific one, set `MDVIEW_BROWSER` (or the
standard `BROWSER`) to a command, e.g. `MDVIEW_BROWSER="open -a Safari"`.
