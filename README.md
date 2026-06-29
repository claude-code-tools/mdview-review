# mdview-review

Render a markdown document in your browser with **Approve / Request-changes** buttons that
report your decision straight back to a Claude Code session — no switching back to the terminal
to type a reply.

A single, self-contained cross-platform Go binary (macOS / Linux / Windows): it renders the
doc, serves it on a random `127.0.0.1` port, opens your browser, and blocks until you click a
button — then prints the verdict and exits. The Claude session runs it in the background and
wakes the moment you decide.

## Install (Claude Code plugin)

```
/plugin marketplace add claude-code-tools/mdview-review
/plugin install mdview-review
```

The skill downloads + checksum-verifies the matching release binary on first use.

## Manual CLI

Download the binary for your platform from the
[latest release](https://github.com/claude-code-tools/mdview-review/releases/latest), then:

```
mdview path/to/file.md
```

It prints `mdview: review server at http://127.0.0.1:PORT/` (open it if your browser didn't),
waits for your click, and prints one line on exit:

- `MDVIEW_VERDICT {"verdict":"approve"}`
- `MDVIEW_VERDICT {"verdict":"changes","comment":"…"}`
- `MDVIEW_VERDICT {"verdict":"dismissed"}`

## Design

See [`docs/design.md`](docs/design.md) for the full design and [`docs/plan.md`](docs/plan.md)
for the implementation plan.

## License

MIT — see [`LICENSE`](LICENSE).
