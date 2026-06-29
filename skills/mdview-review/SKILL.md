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
VER=v0.1.0
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
`%USERPROFILE%\.cache\mdview-review\v0.1.0\mdview.exe` and use that path.

## Step 2 — run it BACKGROUNDED and wait

Run the cached binary on the file **as a background command**:

```
$HOME/.cache/mdview-review/v0.1.0/mdview <path-to-file.md>
```

Run it backgrounded so the wait isn't bounded by any command timeout — it blocks until the
user clicks. The moment they decide, the process exits and you are re-invoked (push, not
polling). Do **not** poll it.

## Step 3 — surface the URL

It prints `mdview: review server at http://127.0.0.1:PORT/` to stderr. Surface that
`http://127.0.0.1:PORT/` link to the user so they can reopen the page if they lose the tab.

## Step 4 — act on the verdict

When the backgrounded command exits, parse the last `MDVIEW_VERDICT` line on stdout:

- `MDVIEW_VERDICT {"verdict":"approve"}` → proceed.
- `MDVIEW_VERDICT {"verdict":"changes","comment":"…"}` → read the comment, make the requested
  changes, and (if useful) re-render the updated doc for another review round.
- `MDVIEW_VERDICT {"verdict":"dismissed"}` → the user closed the tab or didn't decide; ask how
  they'd like to proceed.
