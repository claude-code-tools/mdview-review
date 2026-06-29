---
description: Open a markdown file in the browser with Approve / Request-changes buttons and wait for the verdict.
argument-hint: <path-to-file.md>
---

Use the `mdview-review` skill to render `$ARGUMENTS` for review: ensure the binary is cached
(download + checksum-verify on first run), then run it **backgrounded**, surface the
`http://127.0.0.1:PORT/` URL, and act on the `MDVIEW_VERDICT` line it prints on exit —
`approve` → proceed; `changes` → apply the comment and re-render; `dismissed` → ask how to
proceed.
