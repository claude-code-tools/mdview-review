# Curated Command Buttons Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a row of curated, agent-tailored command buttons at the bottom of the review page; clicking one returns a new `verdict:"command"` outcome (`{command, prompt}`) to the calling agent.

**Architecture:** A `Command` model + built-in defaults live in `internal/render`. `main.go` resolves the set (built-in defaults, or a `MDVIEW_COMMANDS` JSON override) and passes it to `render.Page`, which JSON-injects it into `review.js` (same mechanism as the token). `review.js` builds a horizontally-scrollable pill strip (dynamic both-edge fades), and a click POSTs the command verdict through the unchanged `/verdict` → exit → `MDVIEW_VERDICT` path. The binary never executes commands; it only reports the user's choice.

**Tech Stack:** Go 1.22, stdlib only (`encoding/json`, `strings`, `net/http`). Embedded HTML/CSS/JS assets. goldmark already vendored. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-06-30-curated-command-buttons-design.md`

## Global Constraints

- **No new dependencies** — stdlib only.
- **Push-not-poll preserved** — the process still exits on every resolved review; `MDVIEW_VERDICT {…}` last-stdout-line and exit-0-for-any-outcome are unchanged. A command click resolves exactly like a verdict.
- **Opt-in / backward compatible** — with `MDVIEW_COMMANDS` unset the user gets the built-in 6; existing `approve` / `changes` / `dismissed` outcomes are unchanged in shape.
- **`Command` type lives in `internal/render`** (`render.Command`); `main.go` already imports `render`.
- **`render.Page` gains a `commands []render.Command` parameter** — BOTH call sites in `main.go` (print mode + review mode) and all `Page(` calls in `render_test.go` must be updated.
- **Override = `MDVIEW_COMMANDS`** JSON array of `{id,label,prompt,recommended?}`. **Replace** semantics: unset → built-in 6; `[]` → no strip; non-empty → exactly those. Invalid JSON / entry missing non-empty `id` or `label` → stderr warning + built-in defaults. `recommended` optional, presentational only.
- **Server:** `/verdict` accepts `verdict:"command"` with a non-empty `command` (+ optional `prompt`); empty `command` → 400. 204 on success. Token gate + localhost bind unchanged.
- **`--view` mode** renders no dock → no command strip.
- **Builds for windows/amd64** unchanged (pure Go + assets; no platform primitives).
- **Embedded-asset rule:** editing `internal/render/assets/*` requires rebuilding the binary for `./mdview` to change; `go test` reads the embedded copies, so it sees asset edits without a rebuild.
- **Run server-touching tests under `-race`.**

---

### Task 1: `render.Command` model + built-in defaults

**Files:**
- Create: `internal/render/commands.go`
- Test: `internal/render/commands_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Command struct { ID string `json:"id"`; Label string `json:"label"`; Prompt string `json:"prompt"`; Recommended bool `json:"recommended,omitempty"` }`
  - `func BuiltinCommands() []Command` — the curated default 6, none recommended.

- [ ] **Step 1: Write the failing test**

```go
// internal/render/commands_test.go
package render

import "testing"

func TestBuiltinCommands(t *testing.T) {
	cmds := BuiltinCommands()
	if len(cmds) != 6 {
		t.Fatalf("want 6 builtin commands, got %d", len(cmds))
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		if c.ID == "" || c.Label == "" {
			t.Fatalf("command with empty id/label: %+v", c)
		}
		if c.Prompt == "" {
			t.Fatalf("builtin command %q has empty prompt", c.ID)
		}
		if seen[c.ID] {
			t.Fatalf("duplicate command id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Recommended {
			t.Fatalf("builtin command %q must not be recommended", c.ID)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/gunwoo/Documents/Develop/mdview-review && go test ./internal/render/ -run TestBuiltinCommands`
Expected: FAIL — `undefined: BuiltinCommands`.

- [ ] **Step 3: Write the implementation**

```go
// internal/render/commands.go
package render

// Command is a curated review-action button shown beneath the verdict bar. Clicking it ends
// the review round and returns {verdict:"command", command, prompt} to the calling agent.
type Command struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Prompt      string `json:"prompt"`
	Recommended bool   `json:"recommended,omitempty"` // presentational only: highlight the pill
}

// BuiltinCommands is the curated default set shown when the caller supplies no MDVIEW_COMMANDS.
func BuiltinCommands() []Command {
	return []Command{
		{ID: "review-with-subagent", Label: "Review with subagent", Prompt: "Dispatch an independent subagent to review this document for gaps, risks, and weak assumptions, and report its findings before proceeding."},
		{ID: "devils-advocate", Label: "Stress-test", Prompt: "Challenge this document: surface edge cases, failure modes, and unstated assumptions that could break it, before I proceed."},
		{ID: "simplify", Label: "Simplify", Prompt: "Simplify and tighten this — cut unnecessary scope and complexity without losing essential content — then re-render for review."},
		{ID: "add-detail", Label: "Add detail", Prompt: "Expand the thin or vague parts of this with concrete specifics, then re-render for review."},
		{ID: "verify-against-codebase", Label: "Verify vs codebase", Prompt: "Verify this against the actual codebase — confirm the files, APIs, types, and assumptions it references exist and are accurate, and flag any drift before I proceed."},
		{ID: "explore-alternatives", Label: "Explore alternatives", Prompt: "Lay out 2–3 alternative approaches to what this proposes, with tradeoffs and a recommendation, before I commit to this one."},
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/render/ -run TestBuiltinCommands`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/render/commands.go internal/render/commands_test.go
git commit -m "feat(render): add Command model and built-in default command set"
```

---

### Task 2: server — accept the `command` verdict

**Files:**
- Modify: `internal/server/server.go` (`Verdict` struct, `handleVerdict` switch)
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Verdict` gains `Command string `json:"command,omitempty"`` and `Prompt string `json:"prompt,omitempty"``. `/verdict` accepts `{"verdict":"command","command":<non-empty>,"prompt":<string>}` → 204 + resolves; empty/whitespace `command` → 400.

- [ ] **Step 1: Write the failing tests**

```go
// add to internal/server/server_test.go
func TestCommandVerdict(t *testing.T) {
	h := startTest(t)
	go func() {
		post(t, h.URL+"verdict", "tok", `{"verdict":"command","command":"simplify","prompt":"do the thing"}`)
	}()
	v := h.Wait()
	if v.Verdict != "command" || v.Command != "simplify" || v.Prompt != "do the thing" {
		t.Fatalf("got %+v", v)
	}
}

func TestCommandRequiresNonEmptyCommand(t *testing.T) {
	h := startTest(t)
	if got := post(t, h.URL+"verdict", "tok", `{"verdict":"command","command":"  "}`); got != 400 {
		t.Fatalf("empty command = %d, want 400", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'TestCommandVerdict|TestCommandRequiresNonEmptyCommand'`
Expected: FAIL — `command` falls into the `default` 400 branch, so `TestCommandVerdict` fails (`h.Wait` never sees a command; the POST returns 400) and the struct has no `Command`/`Prompt` fields.

- [ ] **Step 3: Implement the command verdict**

In `internal/server/server.go`, extend the `Verdict` struct:

```go
// Verdict is the outcome of a review, emitted to the session on exit.
type Verdict struct {
	Verdict string `json:"verdict"`
	Comment string `json:"comment,omitempty"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}
```

In `handleVerdict`, add a `case` before the `default:` in the `switch in.Verdict`:

```go
	case "command":
		cmd := strings.TrimSpace(in.Command)
		if cmd == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		v = Verdict{Verdict: "command", Command: cmd, Prompt: in.Prompt}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -race`
Expected: PASS (new tests + all existing).

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): accept command verdict carrying command + prompt"
```

---

### Task 3: render — inject commands + build the strip + wire clicks

**Files:**
- Modify: `internal/render/render.go` (`Page`/`View`/`build` signatures + JSON substitution)
- Modify: `internal/render/assets/review.html` (add `#mdview-commands` container)
- Modify: `internal/render/assets/review.css` (`#mdview-commands` + `.mv-cmd` base styles)
- Modify: `internal/render/assets/review.js` (parse `__MDVIEW_COMMANDS__`, build buttons, wire command clicks)
- Modify: `main.go` (update BOTH `render.Page(...)` call sites to pass commands — defaults for now)
- Test: `internal/render/render_test.go` (update existing `Page(` calls; add new tests)

**Interfaces:**
- Consumes: `render.Command`, `render.BuiltinCommands` (Task 1).
- Produces: `func Page(src []byte, token string, commands []Command) (string, error)`. `build` gains `commands []Command`; `View` passes `nil`. The rendered review page contains `var COMMANDS = <json>;` (placeholder `__MDVIEW_COMMANDS__` replaced) and review.js builds a `.mv-cmd` button per command into `#mdview-commands`, wiring `click → send({verdict:"command", command:id, prompt})`. Empty list → strip hidden.

- [ ] **Step 1: Write/seed the failing tests**

First, update the EXISTING `Page(` calls in `internal/render/render_test.go` to the new 3-arg form (otherwise the package won't compile). For example `Page([]byte("# hi"), "tok")` becomes `Page([]byte("# hi"), "tok", BuiltinCommands())`. Then add:

```go
// add to internal/render/render_test.go
func TestPageInjectsDefaultCommands(t *testing.T) {
	page, err := Page([]byte("# hi"), "tok", BuiltinCommands())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`id="mdview-commands"`,           // the strip container
		"var COMMANDS = [",                // placeholder substituted with a JSON array
		"Review with subagent",            // a default label, present in the injected JSON
		`verdict: "command"`,              // the click wires a command verdict
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	if strings.Contains(page, "__MDVIEW_COMMANDS__") {
		t.Fatal("placeholder __MDVIEW_COMMANDS__ was not substituted")
	}
}

func TestPageCustomCommands(t *testing.T) {
	page, err := Page([]byte("# hi"), "tok", []Command{{ID: "x", Label: "Do X", Prompt: "do x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "Do X") || strings.Contains(page, "Review with subagent") {
		t.Fatalf("custom commands not honored")
	}
}

func TestPageEmptyCommands(t *testing.T) {
	page, err := Page([]byte("# hi"), "tok", []Command{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "var COMMANDS = [];") {
		t.Fatalf("empty commands should inject an empty array")
	}
}

func TestViewHasNoCommandStrip(t *testing.T) {
	page, err := View([]byte("# hi"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "id=\"mdview-commands\"") || strings.Contains(page, "var COMMANDS") {
		t.Fatalf("view mode must not include the command strip")
	}
}
```

Ensure `"strings"` is imported in `render_test.go` (it already is from earlier work).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/render/`
Expected: FAIL — compile error first (existing `Page` calls have 2 args / new tests reference the 3-arg form). After you fix only the existing call arity in Step 1, the new tests fail on missing markup/substitution.

- [ ] **Step 3: Update `render.go` signatures + JSON injection**

In `internal/render/render.go`, add `"encoding/json"` to the imports. Change the signatures:

```go
// Page renders markdown to a full self-contained HTML page WITH the review dock and the
// curated command strip, with the token + commands substituted into the client script.
func Page(src []byte, token string, commands []Command) (string, error) {
	return build(src, true, token, commands)
}

// View renders markdown to a full self-contained HTML page WITHOUT the review dock —
// for showing the user an overview / FYI that needs no decision.
func View(src []byte) (string, error) {
	return build(src, false, "", nil)
}

func build(src []byte, withReview bool, token string, commands []Command) (string, error) {
```

Replace the review-script block near the end of `build`:

```go
	if withReview {
		cj, err := json.Marshal(commands)
		if err != nil {
			return "", err
		}
		if len(commands) == 0 {
			cj = []byte("[]") // json.Marshal(nil) is "null"; the client expects an array
		}
		js := strings.ReplaceAll(reviewJS, "__MDVIEW_TOKEN__", token)
		js = strings.ReplaceAll(js, "__MDVIEW_COMMANDS__", string(cj))
		b.WriteString("<script>")
		b.WriteString(js)
		b.WriteString("</script>")
	}
```

- [ ] **Step 4: Add the strip container to `review.html`**

In `internal/render/assets/review.html`, add the container as the FIRST child of `#mdview-bar`, immediately after the opening `<div id="mdview-bar" ...>` line and before `<div id="mdview-row">`:

```html
  <div id="mdview-commands" role="group" aria-label="Quick commands"></div>
```

- [ ] **Step 5: Add base strip + pill styles to `review.css`**

Append to `internal/render/assets/review.css` (fades + recommended styling come in Task 4):

```css
#mdview-commands{display:flex;align-items:center;gap:8px;overflow-x:auto;
  padding:2px 6px 8px;margin-bottom:6px;border-bottom:1px solid var(--mv-border);
  scrollbar-width:none}
#mdview-commands::-webkit-scrollbar{display:none}
#mdview-bar.panel-open #mdview-commands,#mdview-bar.mv-done #mdview-commands{display:none}
.mv-cmd{flex:none;cursor:pointer;white-space:nowrap;
  font-family:inherit;font-size:12.5px;font-weight:600;line-height:1;
  padding:7px 11px;border-radius:999px;border:1px solid var(--mv-border);
  background:var(--mv-ghost-hover);color:var(--mv-fg);
  transition:background .12s ease,border-color .12s ease,transform .12s ease}
.mv-cmd:hover{background:transparent;border-color:var(--mv-muted);transform:translateY(-1px)}
.mv-cmd:active{transform:translateY(0)}
.mv-cmd:focus-visible{outline:none;box-shadow:0 0 0 3px var(--mv-ring)}
```

- [ ] **Step 6: Build the strip + wire clicks in `review.js`**

In `internal/render/assets/review.js`, add the commands global near the top, right after the `TOKEN` line:

```js
  var COMMANDS = __MDVIEW_COMMANDS__;
```

Then, AFTER the `send` function is defined (it uses `send`), add the builder:

```js
  var cmdStrip = document.getElementById("mdview-commands");
  (function buildCommands() {
    if (!cmdStrip || !Array.isArray(COMMANDS) || COMMANDS.length === 0) {
      if (cmdStrip) cmdStrip.style.display = "none";
      return;
    }
    COMMANDS.forEach(function (cmd) {
      if (!cmd || !cmd.id || !cmd.label) return;
      var b = document.createElement("button");
      b.type = "button";
      b.className = "mv-cmd" + (cmd.recommended ? " mv-cmd--recommended" : "");
      b.textContent = cmd.label;
      b.addEventListener("click", function () {
        send({ verdict: "command", command: cmd.id, prompt: cmd.prompt || "" });
      });
      cmdStrip.appendChild(b);
    });
  })();
```

- [ ] **Step 7: Update the two `render.Page` call sites in `main.go`**

So the program compiles (full `MDVIEW_COMMANDS` parsing arrives in Task 5). In `main.go`:
- Print mode: `page, err := render.Page(src, newToken(), render.BuiltinCommands())`
- Review mode: `page, err := render.Page(src, token, render.BuiltinCommands())`

- [ ] **Step 8: Run tests + sanity-check the binary**

Run:
```bash
go test ./internal/render/ && go build -o mdview . && ./mdview --print assets/demo.md | grep -c 'id="mdview-commands"'
```
Expected: render tests PASS; the `grep -c` prints `1`. (Do not commit the `mdview` binary — it's gitignored.)

- [ ] **Step 9: Commit**

```bash
git add internal/render/render.go internal/render/assets/review.html internal/render/assets/review.css internal/render/assets/review.js internal/render/render_test.go main.go
git commit -m "feat(render): curated command strip — inject commands, build pills, wire command verdict"
```

---

### Task 4: render — dynamic both-edge scroll fades + recommended highlight

**Files:**
- Modify: `internal/render/assets/review.css` (fade masks via state classes; `.mv-cmd--recommended`)
- Modify: `internal/render/assets/review.js` (scroll/resize listener toggling fade state classes)
- Test: `internal/render/render_test.go`

**Interfaces:**
- Consumes: the `#mdview-commands` strip + `.mv-cmd--recommended` class from Task 3.
- Produces: the strip masks its overflowing edge(s) by scroll position (right-only at start, both mid-scroll, left-only at end, none when it fits); recommended pills get an accent style.

- [ ] **Step 1: Write the failing test**

```go
// add to internal/render/render_test.go
func TestPageWiresScrollFades(t *testing.T) {
	page, err := Page([]byte("# hi"), "tok", []Command{{ID: "x", Label: "X", Prompt: "p", Recommended: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`addEventListener("scroll"`, // strip scroll listener
		"is-end",                    // fade state class toggled by the listener
		"mv-cmd--recommended",       // recommended pill style is referenced
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -run TestPageWiresScrollFades`
Expected: FAIL — `addEventListener("scroll"` / `is-end` not present yet (the `mv-cmd--recommended` substring is already emitted by the Task 3 builder, but the other two are missing).

- [ ] **Step 3: Add fade + recommended styles to `review.css`**

Append to `internal/render/assets/review.css`:

```css
/* Scroll-fade: mask whichever edge has hidden overflow (state classes set by review.js). */
#mdview-commands{
  --mv-fade:20px;
  -webkit-mask-image:linear-gradient(to right,transparent 0,#000 var(--mv-fade),#000 calc(100% - var(--mv-fade)),transparent 100%);
  mask-image:linear-gradient(to right,transparent 0,#000 var(--mv-fade),#000 calc(100% - var(--mv-fade)),transparent 100%);
}
#mdview-commands.is-start{
  -webkit-mask-image:linear-gradient(to right,#000 0,#000 calc(100% - var(--mv-fade)),transparent 100%);
  mask-image:linear-gradient(to right,#000 0,#000 calc(100% - var(--mv-fade)),transparent 100%);
}
#mdview-commands.is-end{
  -webkit-mask-image:linear-gradient(to right,transparent 0,#000 var(--mv-fade),#000 100%);
  mask-image:linear-gradient(to right,transparent 0,#000 var(--mv-fade),#000 100%);
}
#mdview-commands.is-start.is-end{-webkit-mask-image:none;mask-image:none}

.mv-cmd--recommended{
  border-color:var(--mv-primary);color:var(--mv-primary);
  background:transparent;font-weight:700}
.mv-cmd--recommended::before{content:"";display:inline-block;width:6px;height:6px;
  margin-right:6px;border-radius:50%;background:var(--mv-primary);vertical-align:middle}
.mv-cmd--recommended:hover{background:var(--mv-ghost-hover);border-color:var(--mv-primary)}
```

(Class meanings: `is-start` = scrolled to the left end, so DON'T fade the left; `is-end` = scrolled to the right end, so DON'T fade the right; both set = no overflow → no fade; neither → both edges fade.)

- [ ] **Step 4: Add the scroll listener to `review.js`**

In `internal/render/assets/review.js`, inside the `buildCommands` IIFE, after the `forEach` that appends buttons (still inside the function, before its closing `})();`), add:

```js
    function updateFades() {
      var maxScroll = cmdStrip.scrollWidth - cmdStrip.clientWidth;
      cmdStrip.classList.toggle("is-start", cmdStrip.scrollLeft <= 1);
      cmdStrip.classList.toggle("is-end", cmdStrip.scrollLeft >= maxScroll - 1);
    }
    cmdStrip.addEventListener("scroll", updateFades);
    window.addEventListener("resize", updateFades);
    updateFades();
```

(When there is no overflow, `maxScroll <= 0`, so both `is-start` and `is-end` are true → mask `none` → no fade.)

- [ ] **Step 5: Run tests + rebuild sanity check**

Run:
```bash
go test ./internal/render/ && go build -o mdview . && ./mdview --print assets/demo.md | grep -c 'addEventListener("scroll"'
```
Expected: render tests PASS; `grep -c` prints `1`. (Don't commit `mdview`.)

- [ ] **Step 6: Commit**

```bash
git add internal/render/assets/review.css internal/render/assets/review.js internal/render/render_test.go
git commit -m "feat(render): dynamic both-edge scroll fades + recommended pill style"
```

---

### Task 5: main — `MDVIEW_COMMANDS` parsing + pass-through + integration test

**Files:**
- Modify: `main.go` (`commandsForReview` helper; review mode passes parsed commands)
- Test: `main_test.go` (parse unit tests + a command-verdict integration round-trip)

**Interfaces:**
- Consumes: `render.Command`, `render.BuiltinCommands`, `render.Page` (Task 3), `rendezvous` (existing).
- Produces: `func commandsForReview() []render.Command` — `MDVIEW_COMMANDS` unset → defaults; valid JSON → those (incl. empty array → none); invalid JSON or any entry with empty `id`/`label` → stderr warning + defaults. Review mode renders with the resolved set.

- [ ] **Step 1: Write the failing tests**

```go
// add to main_test.go
func TestCommandsForReviewDefaults(t *testing.T) {
	t.Setenv("MDVIEW_COMMANDS", "")
	if got := commandsForReview(); len(got) != 6 {
		t.Fatalf("unset MDVIEW_COMMANDS should give 6 defaults, got %d", len(got))
	}
}

func TestCommandsForReviewCustom(t *testing.T) {
	t.Setenv("MDVIEW_COMMANDS", `[{"id":"x","label":"Do X","prompt":"do x","recommended":true}]`)
	got := commandsForReview()
	if len(got) != 1 || got[0].ID != "x" || !got[0].Recommended {
		t.Fatalf("custom commands not parsed: %+v", got)
	}
}

func TestCommandsForReviewEmptyArray(t *testing.T) {
	t.Setenv("MDVIEW_COMMANDS", `[]`)
	if got := commandsForReview(); len(got) != 0 {
		t.Fatalf("empty array should give no commands, got %d", len(got))
	}
}

func TestCommandsForReviewBadJSONFallsBack(t *testing.T) {
	t.Setenv("MDVIEW_COMMANDS", `not json`)
	if got := commandsForReview(); len(got) != 6 {
		t.Fatalf("bad JSON should fall back to 6 defaults, got %d", len(got))
	}
}

func TestCommandsForReviewMissingFieldFallsBack(t *testing.T) {
	t.Setenv("MDVIEW_COMMANDS", `[{"id":"","label":"no id","prompt":"p"}]`)
	if got := commandsForReview(); len(got) != 6 {
		t.Fatalf("entry with empty id should fall back to defaults, got %d", len(got))
	}
}

func TestReviewCommandVerdictRoundTrip(t *testing.T) {
	state := t.TempDir()
	t.Setenv("MDVIEW_STATE_DIR", state)
	bin := buildBin(t)

	md := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(md, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const key = "cmd-itest"

	cmd := exec.Command(bin, md)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Env = append(os.Environ(),
		"MDVIEW_KEY="+key,
		"MDVIEW_STATE_DIR="+state,
		"MDVIEW_BROWSER=true",
		"MDVIEW_NO_CLIENT_SECONDS=30",
		`MDVIEW_COMMANDS=[{"id":"simplify","label":"Simplify","prompt":"tighten it"}]`,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var rec *rendezvous.Record
	for i := 0; i < 100; i++ {
		rec, _ = rendezvous.Read(key)
		if rec != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec == nil {
		_ = cmd.Process.Kill()
		t.Fatal("rendezvous file never appeared")
	}

	url := "http://127.0.0.1:" + strconv.Itoa(rec.Port) + "/verdict"
	body := `{"verdict":"command","command":"simplify","prompt":"tighten it"}`
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+rec.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		resp.Body.Close()
		_ = cmd.Process.Kill()
		t.Fatalf("POST /verdict returned %d", resp.StatusCode)
	}
	resp.Body.Close()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("process should exit 0 on a command verdict: %v", err)
	}
	if !strings.Contains(out.String(), `"verdict":"command"`) ||
		!strings.Contains(out.String(), `"command":"simplify"`) ||
		!strings.Contains(out.String(), `"prompt":"tighten it"`) {
		t.Fatalf("MDVIEW_VERDICT missing command/prompt: %q", out.String())
	}
}
```

Ensure `main_test.go` imports: `bytes`, `net/http`, `os`, `os/exec`, `path/filepath`, `strconv`, `strings`, `testing`, `time`, and `github.com/claude-code-tools/mdview-review/internal/rendezvous`. (`buildBin` already exists from earlier work.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestCommandsForReview|TestReviewCommandVerdictRoundTrip'`
Expected: FAIL — `undefined: commandsForReview`; the round-trip can't yet produce a `command` verdict via the binary (review mode renders fixed defaults from Task 3, but `commandsForReview` doesn't exist).

- [ ] **Step 3: Add `commandsForReview` and use it in review mode**

In `main.go`, add the helper (near `envInt`):

```go
// commandsForReview resolves the command-button set for review mode: MDVIEW_COMMANDS (a JSON
// array of render.Command) replaces the built-in defaults; an empty array disables the strip;
// unset, invalid JSON, or any entry missing a non-empty id/label falls back to the defaults.
func commandsForReview() []render.Command {
	s := os.Getenv("MDVIEW_COMMANDS")
	if s == "" {
		return render.BuiltinCommands()
	}
	var cmds []render.Command
	if err := json.Unmarshal([]byte(s), &cmds); err != nil {
		fmt.Fprintf(os.Stderr, "mdview: ignoring invalid MDVIEW_COMMANDS (%v); using defaults\n", err)
		return render.BuiltinCommands()
	}
	for _, c := range cmds {
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Label) == "" {
			fmt.Fprintln(os.Stderr, "mdview: ignoring MDVIEW_COMMANDS (entry with empty id/label); using defaults")
			return render.BuiltinCommands()
		}
	}
	return cmds // may be an empty slice -> no command strip
}
```

(`json`, `fmt`, `os`, `strings` are already imported in `main.go`.)

Then change the review-mode render call (from Task 3's placeholder) to use it:

```go
	page, err := render.Page(src, token, commandsForReview())
```

(Leave the `--print` caller on `render.BuiltinCommands()`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test . -race && go build -o mdview .`
Expected: PASS; binary builds.

- [ ] **Step 5: Cross-compile check (Windows must still build)**

Run: `GOOS=windows GOARCH=amd64 go build -o /dev/null .`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add main.go main_test.go
git commit -m "feat(main): parse MDVIEW_COMMANDS override and render the resolved command set"
```

---

### Task 6: docs — SKILL (purpose + catalog), CLAUDE, README

**Files:**
- Modify: `skills/mdview-review/SKILL.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the finished behavior from Tasks 1–5.
- Produces: documented `verdict:"command"` handling, the buttons' purpose, the command catalog + dynamic-selection guidance, the `MDVIEW_COMMANDS` override, and the `recommended` flag.

> Do NOT hand-edit the `VER=` version pins in SKILL.md — `scripts/release.sh` bumps them.

- [ ] **Step 1: Add the `command` verdict to SKILL Step 4**

In `skills/mdview-review/SKILL.md`, in "Step 4 — act on the verdict", add a bullet after the `changes` bullet:

```markdown
- `MDVIEW_VERDICT {"verdict":"command","command":"…","prompt":"…"}` → the user clicked a curated
  command button. **Do what `prompt` says** (it's a complete instruction about this document);
  branch on `command` only if you want programmatic dispatch. After acting, re-render the
  updated doc for another review round when appropriate.
```

- [ ] **Step 2: Add the curation guidance + catalog section to SKILL**

In `skills/mdview-review/SKILL.md`, add a new section after Step 4 (before "Overview mode"):

```markdown
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
      $HOME/.cache/mdview-review/v0.1.2/mdview <file.md>

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
```

- [ ] **Step 3: Update CLAUDE.md**

In `CLAUDE.md`, under the architecture/distribution notes, add:

```markdown
**Curated command buttons.** Beneath Approve / Request-changes, the review page shows a
horizontally-scrollable strip of one-click "command" buttons that return a new
`verdict:"command"` outcome (`{command, prompt}`) — the agent executes the returned `prompt`.
The set is the built-in `render.BuiltinCommands()` by default, or a caller-supplied
`MDVIEW_COMMANDS` JSON array (`{id,label,prompt,recommended?}`) that replaces it (`[]` disables).
`Command` lives in `internal/render`; `render.Page` injects the set as JSON into `review.js`,
which builds the pills and POSTs the command verdict through the unchanged `/verdict` path.
`--view` mode has no strip. The SKILL carries a catalog + guidance so agents tailor the set per
document.
```

- [ ] **Step 4: Update README.md**

In `README.md`, near the env-overrides paragraph, add:

```markdown
`MDVIEW_COMMANDS` (review mode) sets the curated command buttons shown beneath Approve /
Request-changes — a JSON array of `{id,label,prompt,recommended?}` that replaces the built-in
set (`[]` disables the strip). Clicking one returns `MDVIEW_VERDICT {"verdict":"command",...}`
to the caller; the agent acts on the button's `prompt`.
```

- [ ] **Step 5: Verify docs reference reality**

Run: `go build -o mdview . && MDVIEW_COMMANDS='[]' ./mdview --print assets/demo.md | grep -c 'var COMMANDS = \[\];'`
Expected: prints `1` (the documented `[]`-disables behavior is real in print mode too). (Don't commit `mdview`.)

- [ ] **Step 6: Commit**

```bash
git add skills/mdview-review/SKILL.md CLAUDE.md README.md
git commit -m "docs: document curated command buttons, MDVIEW_COMMANDS, and the catalog guidance"
```

---

## After all tasks

- Full suite + vet: `go test ./... -race` (expected: all PASS) and `go vet ./...` (clean).
- Windows cross-build: `GOOS=windows GOARCH=amd64 go build -o /dev/null .` (exit 0).
- Manual e2e (the unit tests can't cover the visual strip): run
  `MDVIEW_KEY=manual MDVIEW_OWNER_PID=$PPID ./mdview assets/demo.md`, confirm the strip renders
  with the 6 default pills, scrolls with edge fades, and clicking one prints
  `MDVIEW_VERDICT {"verdict":"command",...}`; then try
  `MDVIEW_COMMANDS='[{"id":"x","label":"Do X","prompt":"do x","recommended":true}]' ./mdview assets/demo.md`
  and confirm a single highlighted pill, and `MDVIEW_COMMANDS='[]'` shows no strip.
- **Release** (separate from this plan): once merged, cut a release with `scripts/release.sh`.

## Self-Review

**Spec coverage:**
- `Command` model + built-in 6 → Task 1. ✓
- `command` verdict (server accept, 204, empty→400, payload) → Task 2. ✓
- Injection (`Page` gains commands; JSON-substitute) + strip markup + pill build + click wiring → Task 3. ✓
- Dynamic both-edge scroll fades + `recommended` highlight → Task 4. ✓
- `MDVIEW_COMMANDS` parse (replace / `[]` / bad→defaults) + pass-through + integration → Task 5. ✓
- SKILL purpose + catalog + dynamic guidance + `recommended`; CLAUDE/README → Task 6. ✓
- `--view` has no strip → Task 3 (`View` passes nil; test `TestViewHasNoCommandStrip`). ✓
- Backward compatible (unset → defaults; approve/changes unchanged) → Tasks 3+5. ✓
- Windows builds → Task 5 Step 5. ✓
- Security (token gate unchanged; JSON injection; textContent labels) → Task 2 (gate) + Task 3 (JSON/textContent). ✓

**Placeholder scan:** none — every code step shows complete code.

**Type consistency:** `render.Command{ID,Label,Prompt,Recommended}` is used identically across Tasks 1/3/5; `render.Page(src, token, commands)` matches in render.go (Task 3) and both main.go callers (Tasks 3+5); `Verdict{Command,Prompt}` matches between Task 2 and the Task 5 round-trip assertions; review.js placeholders `__MDVIEW_TOKEN__`/`__MDVIEW_COMMANDS__` match the render.go substitutions; the fade state classes `is-start`/`is-end` match between the CSS (Task 4 Step 3) and the JS listener (Task 4 Step 4).
