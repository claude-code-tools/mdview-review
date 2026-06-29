# mdview-review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `mdview` — a cross-platform Go binary that renders a markdown file in the browser with in-page Approve / Request-changes buttons and reports the verdict back to a waiting Claude Code session — distributed as a Claude plugin from `claude-code-tools/mdview-review`.

**Architecture:** A single static Go binary renders markdown (goldmark) → assembles a self-contained HTML page (CSS + review UI + mermaid embedded via `go:embed`) → serves it on `127.0.0.1:<random port>` → opens the browser → blocks until the user clicks a button (POST) or the tab closes → prints one `MDVIEW_VERDICT {json}` line to stdout → exits. The Claude session runs it **backgrounded** and is re-invoked on exit (proven). The repo doubles as a single-plugin marketplace; CI cross-compiles release binaries the skill downloads on first run.

**Tech Stack:** Go 1.22 (stdlib `net/http`, `crypto`, `os/exec`; `github.com/yuin/goldmark` for markdown), `go:embed` for assets, GitHub Actions for CI/release, Claude Code plugin/skill/marketplace manifests.

## Global Constraints

- **Bind `127.0.0.1` only.** Never expose off-host.
- **Random ephemeral port** via `net.Listen("tcp", "127.0.0.1:0")` — never hardcode a port.
- **Pure Go, no cgo** — so `GOOS`/`GOARCH` cross-compile every target from one CI runner.
- **No runtime dependencies** — all assets embedded; the binary runs with nothing installed.
- **Output contract (stdout, exactly one line):** `MDVIEW_VERDICT {"verdict":"approve"}` |
  `MDVIEW_VERDICT {"verdict":"changes","comment":"<text>"}` | `MDVIEW_VERDICT {"verdict":"dismissed"}`.
- **Exit code 0** for every captured outcome (approve/changes/dismissed); non-zero only for real errors.
- **Token:** random per-run `crypto/rand` 16-byte hex; required on `POST /verdict` (403 on mismatch).
- **Verdicts:** `approve` | `changes` (non-empty comment required) | `dismissed`. Nothing else.
- **Module path:** `github.com/claude-code-tools/mdview-review`. **Binary name:** `mdview`.
- **Org/repo already exist decision:** GitHub org `claude-code-tools` (created); repo to be created in Task 1.
- **No git in `~/.claude`/`~/.config`** — the only repo is the new `mdview-review` clone; commit there.

---

### Task 1: Create the repo and Go module skeleton

**Files:**
- Create (remote+local): `claude-code-tools/mdview-review` cloned to `~/Documents/Develop/mdview-review`
- Create: `~/Documents/Develop/mdview-review/go.mod`
- Create: `~/Documents/Develop/mdview-review/LICENSE` (MIT)
- Create: `~/Documents/Develop/mdview-review/.gitignore`
- Create: `~/Documents/Develop/mdview-review/README.md`

**Interfaces:**
- Produces: a Go module rooted at `github.com/claude-code-tools/mdview-review`, cloned locally, pushed to GitHub with topics set.

- [ ] **Step 1: Create + clone the repo**

```bash
gh repo create claude-code-tools/mdview-review --public \
  --description "Render a markdown doc with in-page Approve / Request-changes buttons that report the verdict to a Claude Code session" \
  --clone
# clones into ./mdview-review under the current dir; ensure cwd is ~/Documents/Develop
mv ./mdview-review ~/Documents/Develop/ 2>/dev/null || true
ls ~/Documents/Develop/mdview-review/.git >/dev/null && echo "cloned OK"
```
Expected: `cloned OK`. (If `gh repo create` errors on org permission, run `gh auth refresh -s admin:org -h github.com` and retry.)

- [ ] **Step 2: Set repo topics**

```bash
gh repo edit claude-code-tools/mdview-review \
  --add-topic claude-code --add-topic claude-code-plugin --add-topic markdown-review
```
Expected: prints the updated repo URL.

- [ ] **Step 3: Initialize the Go module**

```bash
cd ~/Documents/Develop/mdview-review
go mod init github.com/claude-code-tools/mdview-review
go get github.com/yuin/goldmark@latest
```
Expected: `go.mod` created; goldmark added to `go.mod` + `go.sum`.

- [ ] **Step 4: Add LICENSE, .gitignore, README**

`LICENSE` — standard MIT text (year 2026, "Gunwoo Lee"). `.gitignore`:
```
/dist/
mdview
mdview.exe
*.out
```
`README.md` (minimal for now):
```markdown
# mdview-review

Render a markdown document in your browser with **Approve / Request-changes** buttons that
report your decision straight back to a Claude Code session.

Install (Claude Code plugin):

    /plugin marketplace add claude-code-tools/mdview-review
    /plugin install mdview-review

See `docs/design.md` for the full design.
```

- [ ] **Step 5: Commit**

```bash
cd ~/Documents/Develop/mdview-review
git add -A && git commit -m "chore: scaffold Go module, license, readme"
git push -u origin main
```
Expected: push succeeds to `claude-code-tools/mdview-review`.

---

### Task 2: `render` package — markdown → self-contained HTML page

**Files:**
- Create: `internal/render/render.go`
- Create: `internal/render/render_test.go`
- Create: `internal/render/assets/github-markdown.css`
- Create: `internal/render/assets/mermaid.min.js`
- Create: `internal/render/assets/review.css`
- Create: `internal/render/assets/review.html`
- Create: `internal/render/assets/review.js`

**Interfaces:**
- Produces: `func render.Page(src []byte, token string) (string, error)` — full HTML page with
  GitHub CSS + review UI inlined, `__MDVIEW_TOKEN__` in the client script replaced by `token`,
  and mermaid injected only when the doc contains a ```` ```mermaid ```` block.

- [ ] **Step 1: Vendor the static assets**

```bash
cd ~/Documents/Develop/mdview-review/internal/render/assets
curl -fsSL https://cdn.jsdelivr.net/npm/github-markdown-css@5/github-markdown.min.css -o github-markdown.css
curl -fsSL https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js -o mermaid.min.js
ls -la github-markdown.css mermaid.min.js
```
Expected: both files present and non-empty.

- [ ] **Step 2: Write the review UI assets**

`review.html`:
```html
<div id="mdview-bar">
  <button id="mdview-approve" type="button">✅ Approve</button>
  <button id="mdview-changes" type="button">✏️ Request changes</button>
  <div id="mdview-panel">
    <label for="mdview-comment">What should change?</label>
    <textarea id="mdview-comment" placeholder="Describe the changes…"></textarea>
    <div id="mdview-panel-actions">
      <button id="mdview-submit" type="button" disabled>Submit feedback</button>
      <button id="mdview-cancel" type="button">Cancel</button>
    </div>
  </div>
</div>
```

`review.css`:
```css
#mdview-bar{position:fixed;left:0;right:0;bottom:0;z-index:99999;display:flex;gap:12px;
  align-items:flex-start;padding:12px 20px;background:#fff;border-top:1px solid #d0d7de;
  box-shadow:0 -2px 12px rgba(0,0,0,.08);font:14px -apple-system,system-ui,sans-serif;}
#mdview-bar button{font:600 14px -apple-system,system-ui,sans-serif;padding:8px 16px;
  border-radius:6px;border:1px solid #d0d7de;cursor:pointer;}
#mdview-approve{background:#1f883d;color:#fff;border-color:#1a7f37;}
#mdview-changes{background:#f6f8fa;color:#24292f;}
#mdview-panel{display:none;flex-direction:column;gap:8px;flex:1;}
#mdview-panel.open{display:flex;}
#mdview-comment{width:100%;min-height:64px;padding:8px;border-radius:6px;border:1px solid #d0d7de;
  font:14px -apple-system,system-ui,sans-serif;resize:vertical;box-sizing:border-box;}
#mdview-panel-actions{display:flex;gap:8px;}
#mdview-status{color:#57606a;font:14px -apple-system,system-ui,sans-serif;}
```

`review.js`:
```javascript
(function () {
  var TOKEN = "__MDVIEW_TOKEN__";
  var bar = document.getElementById("mdview-bar");
  var approve = document.getElementById("mdview-approve");
  var changes = document.getElementById("mdview-changes");
  var panel = document.getElementById("mdview-panel");
  var comment = document.getElementById("mdview-comment");
  var submit = document.getElementById("mdview-submit");
  var cancel = document.getElementById("mdview-cancel");
  var done = false;
  try { new EventSource("/events"); } catch (e) {}
  function setStatus(msg) {
    bar.innerHTML = "";
    var s = document.createElement("div");
    s.id = "mdview-status"; s.textContent = msg; bar.appendChild(s);
  }
  function send(payload) {
    if (done) return; done = true;
    fetch("/verdict", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + TOKEN },
      body: JSON.stringify(payload)
    }).then(function (r) {
      if (r.ok) setStatus("✓ Sent — you can close this tab.");
      else { done = false; setStatus("Could not send (HTTP " + r.status + ")."); }
    }).catch(function () { setStatus("Review session ended."); });
  }
  approve.addEventListener("click", function () { send({ verdict: "approve" }); });
  changes.addEventListener("click", function () {
    panel.classList.add("open"); approve.style.display = "none"; changes.style.display = "none";
    comment.focus();
  });
  cancel.addEventListener("click", function () {
    panel.classList.remove("open"); approve.style.display = ""; changes.style.display = "";
  });
  comment.addEventListener("input", function () { submit.disabled = comment.value.trim() === ""; });
  function doSubmit() { var c = comment.value.trim(); if (c) send({ verdict: "changes", comment: c }); }
  submit.addEventListener("click", doSubmit);
  comment.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); doSubmit(); }
    if (e.key === "Escape") { cancel.click(); }
  });
})();
```

- [ ] **Step 3: Write the failing test**

`internal/render/render_test.go`:
```go
package render

import "strings"
import "testing"

func TestPageInjectsTokenAndBar(t *testing.T) {
	out, err := Page([]byte("# Title\n\nHello"), "deadbeef")
	if err != nil { t.Fatal(err) }
	for _, want := range []string{`id="mdview-bar"`, "deadbeef", "<h1", "Hello"} {
		if !strings.Contains(out, want) { t.Errorf("missing %q", want) }
	}
	if strings.Contains(out, "__MDVIEW_TOKEN__") { t.Error("placeholder not replaced") }
}

func TestPageMermaidConditional(t *testing.T) {
	plain, _ := Page([]byte("# x"), "t")
	if strings.Contains(plain, "mermaid.initialize") { t.Error("mermaid injected without fence") }
	withM, _ := Page([]byte("```mermaid\ngraph TD;A-->B\n```\n"), "t")
	if !strings.Contains(withM, "mermaid.initialize") { t.Error("mermaid not injected with fence") }
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd ~/Documents/Develop/mdview-review && go test ./internal/render/ -v`
Expected: FAIL — `undefined: Page`.

- [ ] **Step 5: Implement `render.go`**

```go
package render

import (
	"bytes"
	_ "embed"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed assets/github-markdown.css
var githubCSS string

//go:embed assets/mermaid.min.js
var mermaidJS string

//go:embed assets/review.css
var reviewCSS string

//go:embed assets/review.html
var reviewHTML string

//go:embed assets/review.js
var reviewJS string

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Page renders markdown source to a full self-contained HTML page with the review UI
// injected and the token substituted into the client script.
func Page(src []byte, token string) (string, error) {
	var bodyBuf bytes.Buffer
	if err := md.Convert(src, &bodyBuf); err != nil {
		return "", err
	}
	body := bodyBuf.String()
	hasMermaid := strings.Contains(body, "language-mermaid")

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>mdview review</title><style>`)
	b.WriteString(githubCSS)
	b.WriteString("\nbody{box-sizing:border-box;margin:0;padding:2.5rem clamp(1rem,5vw,5rem) 110px;}")
	b.WriteString("\n.mermaid{margin:1rem 0;}\n")
	b.WriteString(reviewCSS)
	b.WriteString(`</style></head><body class="markdown-body">`)
	b.WriteString(body)
	b.WriteString(reviewHTML)
	if hasMermaid {
		b.WriteString("<script>")
		b.WriteString(strings.ReplaceAll(mermaidJS, "</script", `<\/script`))
		b.WriteString("</script><script>")
		b.WriteString(`document.querySelectorAll("pre>code.language-mermaid").forEach(function(c){var d=document.createElement("div");d.className="mermaid";d.textContent=c.textContent;c.parentElement.replaceWith(d);});`)
		b.WriteString(`mermaid.initialize({startOnLoad:false,theme:"default",securityLevel:"loose",maxTextSize:1000000,maxEdges:5000});mermaid.run({querySelector:".mermaid"});`)
		b.WriteString("</script>")
	}
	b.WriteString("<script>")
	b.WriteString(strings.ReplaceAll(reviewJS, "__MDVIEW_TOKEN__", token))
	b.WriteString("</script></body></html>")
	return b.String(), nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd ~/Documents/Develop/mdview-review && go test ./internal/render/ -v`
Expected: PASS (both tests).

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(render): markdown to self-contained HTML page with review UI"
```

---

### Task 3: `server` package — routes, verdict capture, decide-once

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces:
  - `type Verdict struct { Verdict string `json:"verdict"`; Comment string `json:"comment,omitempty"` }`
  - `type Options struct { Page, Token string; NoClientTimeout, MaxLifetime, PPIDPoll, TabCloseGrace time.Duration }`
  - `func Start(o Options) (*Handle, error)` — starts the server on a random localhost port.
  - `func (h *Handle) Wait() Verdict` — blocks until decided.
  - `func (h *Handle) Close() error` — shuts the server down (test cleanup).
  - `h.Port int`, `h.URL string`.

- [ ] **Step 1: Write the failing test (routes + verdict)**

`internal/server/server_test.go`:
```go
package server

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func startTest(t *testing.T) *Handle {
	t.Helper()
	h, err := Start(Options{
		Page: `<html><body><div id="mdview-bar"></div>tok</body></html>`,
		Token: "tok", NoClientTimeout: time.Hour, MaxLifetime: time.Hour,
	})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { h.Close() })
	return h
}

func post(t *testing.T, url, auth, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" { req.Header.Set("Authorization", "Bearer "+auth) }
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestServesPageAnd404(t *testing.T) {
	h := startTest(t)
	resp, err := http.Get(h.URL)
	if err != nil { t.Fatal(err) }
	if resp.StatusCode != 200 { t.Fatalf("GET / = %d", resp.StatusCode) }
	resp2, _ := http.Get(h.URL + "nope")
	if resp2.StatusCode != 404 { t.Fatalf("GET /nope = %d", resp2.StatusCode) }
}

func TestVerdictTokenGate(t *testing.T) {
	h := startTest(t)
	if got := post(t, h.URL+"verdict", "", `{"verdict":"approve"}`); got != 403 {
		t.Fatalf("no token = %d, want 403", got)
	}
	if got := post(t, h.URL+"verdict", "wrong", `{"verdict":"approve"}`); got != 403 {
		t.Fatalf("wrong token = %d, want 403", got)
	}
}

func TestApprove(t *testing.T) {
	h := startTest(t)
	go func() { post(t, h.URL+"verdict", "tok", `{"verdict":"approve"}`) }()
	v := h.Wait()
	if v.Verdict != "approve" { t.Fatalf("got %+v", v) }
}

func TestChangesRequiresComment(t *testing.T) {
	h := startTest(t)
	if got := post(t, h.URL+"verdict", "tok", `{"verdict":"changes","comment":"  "}`); got != 400 {
		t.Fatalf("empty comment = %d, want 400", got)
	}
	go func() { post(t, h.URL+"verdict", "tok", `{"verdict":"changes","comment":"fix title"}`) }()
	v := h.Wait()
	if v.Verdict != "changes" || v.Comment != "fix title" { t.Fatalf("got %+v", v) }
}

func TestInvalidVerdict(t *testing.T) {
	h := startTest(t)
	if got := post(t, h.URL+"verdict", "tok", `{"verdict":"nope"}`); got != 400 {
		t.Fatalf("invalid = %d, want 400", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/Documents/Develop/mdview-review && go test ./internal/server/ -v`
Expected: FAIL — `undefined: Start` etc.

- [ ] **Step 3: Implement `server.go` (routes + decide; lifecycle stubs added in Task 4)**

```go
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Verdict struct {
	Verdict string `json:"verdict"`
	Comment string `json:"comment,omitempty"`
}

type Options struct {
	Page            string
	Token           string
	NoClientTimeout time.Duration
	MaxLifetime     time.Duration
	PPIDPoll        time.Duration
	TabCloseGrace   time.Duration
}

type Handle struct {
	Port int
	URL  string

	srv   *http.Server
	token string
	page  string
	grace time.Duration

	mu            sync.Mutex
	decided       bool
	clients       int
	everConnected bool
	tabTimer      *time.Timer

	result chan Verdict
	stop   chan struct{}
}

func Start(o Options) (*Handle, error) {
	if o.NoClientTimeout == 0 { o.NoClientTimeout = 60 * time.Second }
	if o.MaxLifetime == 0 { o.MaxLifetime = 6 * time.Hour }
	if o.PPIDPoll == 0 { o.PPIDPoll = time.Second }
	if o.TabCloseGrace == 0 { o.TabCloseGrace = time.Second }

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { return nil, err }
	port := ln.Addr().(*net.TCPAddr).Port

	h := &Handle{
		Port:   port,
		URL:    fmt.Sprintf("http://127.0.0.1:%d/", port),
		token:  o.Token,
		page:   o.Page,
		grace:  o.TabCloseGrace,
		result: make(chan Verdict, 1),
		stop:   make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleRoot)
	mux.HandleFunc("/events", h.handleEvents)
	mux.HandleFunc("/verdict", h.handleVerdict)
	h.srv = &http.Server{Handler: mux}
	go h.srv.Serve(ln)
	go h.lifecycle(o)
	return h, nil
}

func (h *Handle) Wait() Verdict { return <-h.result }

func (h *Handle) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return h.srv.Shutdown(ctx)
}

func (h *Handle) decide(v Verdict) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.decided { return }
	h.decided = true
	h.result <- v
	close(h.stop)
}

func (h *Handle) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" { w.WriteHeader(http.StatusNotFound); return }
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, h.page)
}

func (h *Handle) handleVerdict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.WriteHeader(http.StatusNotFound); return }
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var in Verdict
	_ = json.Unmarshal(body, &in)
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == "" {
		var raw map[string]any
		if json.Unmarshal(body, &raw) == nil {
			if t, ok := raw["token"].(string); ok { provided = t }
		}
	}
	if provided != h.token { w.WriteHeader(http.StatusForbidden); return }
	switch in.Verdict {
	case "approve":
		w.WriteHeader(http.StatusNoContent)
		h.decide(Verdict{Verdict: "approve"})
	case "changes":
		c := strings.TrimSpace(in.Comment)
		if c == "" { w.WriteHeader(http.StatusBadRequest); return }
		w.WriteHeader(http.StatusNoContent)
		h.decide(Verdict{Verdict: "changes", Comment: c})
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

// handleEvents and lifecycle are completed in Task 4. Minimal stubs so the package builds:
func (h *Handle) handleEvents(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
func (h *Handle) lifecycle(o Options)                                 { <-h.stop }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/Documents/Develop/mdview-review && go test ./internal/server/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(server): routes + token-gated verdict capture"
```

---

### Task 4: `server` lifecycle — SSE, tab-close, no-client, max-lifetime, ppid

**Files:**
- Modify: `internal/server/server.go` (replace the `handleEvents` + `lifecycle` stubs)
- Modify: `internal/server/server_test.go` (add lifecycle tests)

**Interfaces:**
- Consumes: `Start`, `Handle`, `Verdict` from Task 3.
- Produces: dismissed-verdict behavior on SSE disconnect, no client, and max-lifetime.

- [ ] **Step 1: Write the failing tests**

Append to `internal/server/server_test.go`:
```go
import "context" // add to existing import block

func TestNoClientBackstop(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: 50 * time.Millisecond, MaxLifetime: time.Hour})
	t.Cleanup(func() { h.Close() })
	if v := h.Wait(); v.Verdict != "dismissed" { t.Fatalf("got %+v", v) }
}

func TestMaxLifetime(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: time.Hour, MaxLifetime: 50 * time.Millisecond})
	t.Cleanup(func() { h.Close() })
	if v := h.Wait(); v.Verdict != "dismissed" { t.Fatalf("got %+v", v) }
}

func TestTabClose(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour, TabCloseGrace: 30 * time.Millisecond})
	t.Cleanup(func() { h.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", h.URL+"events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	if resp.StatusCode != 200 { t.Fatalf("events = %d", resp.StatusCode) }
	cancel() // simulate tab close
	if v := h.Wait(); v.Verdict != "dismissed" { t.Fatalf("got %+v", v) }
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/Documents/Develop/mdview-review && go test ./internal/server/ -run 'TestNoClientBackstop|TestMaxLifetime|TestTabClose' -v`
Expected: FAIL (stubs never produce `dismissed`; tests block then time out / fail).

- [ ] **Step 3: Replace the stubs in `server.go`**

```go
import "os" // add to the import block

func (h *Handle) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok { http.Error(w, "no flusher", http.StatusInternalServerError); return }
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	io.WriteString(w, ": connected\n\n")
	fl.Flush()

	h.mu.Lock()
	h.everConnected = true
	h.clients++
	if h.tabTimer != nil { h.tabTimer.Stop(); h.tabTimer = nil }
	h.mu.Unlock()

	<-r.Context().Done() // client disconnected

	h.mu.Lock()
	h.clients--
	if h.clients <= 0 && !h.decided {
		h.tabTimer = time.AfterFunc(h.grace, func() {
			h.mu.Lock()
			fire := h.clients <= 0 && !h.decided
			h.mu.Unlock()
			if fire { h.decide(Verdict{Verdict: "dismissed"}) }
		})
	}
	h.mu.Unlock()
}

func (h *Handle) lifecycle(o Options) {
	noClient := time.NewTimer(o.NoClientTimeout)
	maxLife := time.NewTimer(o.MaxLifetime)
	ppid := time.NewTicker(o.PPIDPoll)
	defer noClient.Stop()
	defer maxLife.Stop()
	defer ppid.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-noClient.C:
			h.mu.Lock()
			ec := h.everConnected
			h.mu.Unlock()
			if !ec { h.decide(Verdict{Verdict: "dismissed"}) }
		case <-maxLife.C:
			h.decide(Verdict{Verdict: "dismissed"})
		case <-ppid.C:
			if os.Getppid() == 1 { h.decide(Verdict{Verdict: "dismissed"}) } // POSIX; no-op on Windows
		}
	}
}
```

- [ ] **Step 4: Run all server tests**

Run: `cd ~/Documents/Develop/mdview-review && go test ./internal/server/ -v`
Expected: PASS (8 tests). Also run `go test ./... && go vet ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(server): SSE tab-close + no-client + max-lifetime + ppid cleanup"
```

---

### Task 5: `main.go` — CLI wiring + browser open

**Files:**
- Create: `main.go`

**Interfaces:**
- Consumes: `render.Page`, `server.Start`/`Wait` from Tasks 2–4.
- Produces: the `mdview` binary behavior: stderr URL line, `MDVIEW_VERDICT` stdout line, exit 0.

- [ ] **Step 1: Implement `main.go`**

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/claude-code-tools/mdview-review/internal/render"
	"github.com/claude-code-tools/mdview-review/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mdview <file.md>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}
	token := hex.EncodeToString(tok)

	page, err := render.Page(src, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: render: %v\n", err)
		os.Exit(1)
	}

	h, err := server.Start(server.Options{
		Page:            page,
		Token:           token,
		NoClientTimeout: envDur("MDVIEW_NO_CLIENT_SECONDS", 60),
		MaxLifetime:     envDur("MDVIEW_MAX_LIFETIME_SECONDS", 6*3600),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "mdview: review server at %s\n", h.URL)
	openBrowser(h.URL)

	v := h.Wait()
	out, _ := json.Marshal(v)
	fmt.Printf("MDVIEW_VERDICT %s\n", out)
	os.Exit(0)
}

func envDur(name string, defSeconds float64) time.Duration {
	if s := os.Getenv(name); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return time.Duration(f * float64(time.Second))
		}
	}
	return time.Duration(defSeconds * float64(time.Second))
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
```

- [ ] **Step 2: Build**

Run: `cd ~/Documents/Develop/mdview-review && go build -o mdview . && echo built`
Expected: `built`; `./mdview` exists.

- [ ] **Step 3: Manual smoke (stub the browser opener so nothing pops up; drive it with curl)**

```bash
cd ~/Documents/Develop/mdview-review
printf '# Hello\n\nReview me.\n' > /tmp/smoke.md
mkdir -p /tmp/mdstub && printf '#!/bin/sh\necho "$@" >> /tmp/mdstub/opened\n' > /tmp/mdstub/open && chmod +x /tmp/mdstub/open
PATH="/tmp/mdstub:$PATH" ./mdview /tmp/smoke.md > /tmp/mdout.log 2> /tmp/mderr.log &
sleep 1
URL=$(sed -n 's/.*review server at \(http[^ ]*\).*/\1/p' /tmp/mderr.log); echo "URL=$URL"
TOKEN=$(curl -s "$URL" | sed -n 's/.*var TOKEN = "\([0-9a-f]*\)".*/\1/p'); echo "TOKEN=${TOKEN:0:8}…"
curl -s -o /dev/null -w "%{http_code}\n" -X POST "${URL}verdict" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"verdict":"approve"}'
wait
cat /tmp/mdout.log
```
Expected: `204` from curl, and `/tmp/mdout.log` contains `MDVIEW_VERDICT {"verdict":"approve"}`; `/tmp/mdstub/opened` has the URL.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: mdview CLI — render, serve, open browser, emit verdict"
```

---

### Task 6: CI + release workflows

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Produces: CI that vets/tests/builds on push & PR; a tag-triggered release that publishes
  `mdview-<os>-<arch>[.exe]` + `SHA256SUMS` to a GitHub Release.

- [ ] **Step 1: Write `ci.yml`**

```yaml
name: ci
on:
  push: { branches: [main] }
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: go vet ./...
      - run: go test ./...
      - run: go build ./...
```

- [ ] **Step 2: Write `release.yml`**

```yaml
name: release
on:
  push:
    tags: ['v*']
permissions:
  contents: write
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - name: Build all targets
        run: |
          mkdir -p dist
          targets="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64"
          for t in $targets; do
            os="${t%/*}"; arch="${t#*/}"; out="mdview-$os-$arch"
            [ "$os" = "windows" ] && out="$out.exe"
            CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "dist/$out" .
          done
          cd dist && shasum -a 256 mdview-* > SHA256SUMS && cat SHA256SUMS
      - name: Publish release
        uses: softprops/action-gh-release@v2
        with:
          files: dist/*
```

- [ ] **Step 3: Validate workflow YAML locally (syntax)**

Run: `cd ~/Documents/Develop/mdview-review && python3 -c "import yaml,glob;[yaml.safe_load(open(f)) for f in glob.glob('.github/workflows/*.yml')];print('yaml ok')"`
Expected: `yaml ok`.

- [ ] **Step 4: Commit + push, confirm CI goes green**

```bash
git add -A && git commit -m "ci: test workflow + tagged release pipeline"
git push
gh run watch "$(gh run list --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status || gh run view --log-failed
```
Expected: the `ci` run completes successfully.

---

### Task 7: Plugin, marketplace manifest, skill, and `/mdview` command

**Files:**
- Create: `.claude-plugin/marketplace.json`
- Create: `.claude-plugin/plugin.json`
- Create: `skills/mdview-review/SKILL.md`
- Create: `commands/mdview.md`

**Interfaces:**
- Consumes: the release-asset naming (`mdview-<os>-<arch>[.exe]`) + `SHA256SUMS` from Task 6.
- Produces: an installable plugin (`/plugin marketplace add claude-code-tools/mdview-review`).

- [ ] **Step 1: Write `.claude-plugin/plugin.json`**

```json
{
  "$schema": "https://anthropic.com/claude-code/plugin.schema.json",
  "name": "mdview-review",
  "version": "0.1.0",
  "description": "Render a markdown document in the browser with Approve / Request-changes buttons whose verdict is reported back to the Claude Code session.",
  "author": { "name": "Gunwoo Lee" },
  "homepage": "https://github.com/claude-code-tools/mdview-review"
}
```

- [ ] **Step 2: Write `.claude-plugin/marketplace.json`**

```json
{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "claude-code-tools",
  "description": "Gunwoo's Claude Code tools",
  "owner": { "name": "Gunwoo Lee" },
  "plugins": [
    {
      "name": "mdview-review",
      "description": "Markdown review with in-page Approve / Request-changes buttons that report the verdict to the session.",
      "author": { "name": "Gunwoo Lee" },
      "category": "productivity",
      "source": "./"
    }
  ]
}
```

- [ ] **Step 3: Write `skills/mdview-review/SKILL.md`**

````markdown
---
name: mdview-review
description: Use whenever you are about to ask the user to read, review, or approve a markdown document — a spec, design doc, or implementation plan. Renders it in the browser with in-page Approve / Request-changes buttons and returns the user's verdict to the session. Use this instead of telling the user to open a .md file.
---

# mdview-review

Render a markdown file in the browser with **Approve / Request-changes** buttons and get the
user's decision back, in one step.

## When to use

Whenever you would otherwise tell the user to open or read a `.md` file for review/approval
(a spec, plan, design doc), use this skill instead.

## How to run it

1. **Ensure the binary is available** (first run only). Check the cache; if missing, download
   the matching release asset, verify its checksum, and cache it:

   ```bash
   VER=v0.1.0
   DIR="$HOME/.cache/mdview-review/$VER"
   OS=$(uname -s | tr '[:upper:]' '[:lower:]'); case "$OS" in darwin) OS=darwin;; linux) OS=linux;; *) OS=windows;; esac
   ARCH=$(uname -m); case "$ARCH" in x86_64|amd64) ARCH=amd64;; arm64|aarch64) ARCH=arm64;; esac
   BIN="mdview-$OS-$ARCH"; [ "$OS" = windows ] && BIN="$BIN.exe"
   if [ ! -x "$DIR/mdview" ]; then
     mkdir -p "$DIR"
     base="https://github.com/claude-code-tools/mdview-review/releases/download/$VER"
     curl -fsSL "$base/$BIN" -o "$DIR/mdview"
     curl -fsSL "$base/SHA256SUMS" -o "$DIR/SHA256SUMS"
     grep " $BIN\$" "$DIR/SHA256SUMS" | sed "s# .*# $DIR/mdview#" | shasum -a 256 -c -
     chmod +x "$DIR/mdview"
   fi
   ```
   (On Windows without `uname`, download `mdview-windows-amd64.exe` and run it directly.)

2. **Run it BACKGROUNDED** and wait — it blocks until the user decides:
   `~/.cache/mdview-review/v0.1.0/mdview <path-to-file.md>`
   Run it as a background command so the wait isn't bound by any command timeout.

3. **Surface the URL.** It prints `mdview: review server at http://127.0.0.1:PORT/` to stderr —
   give the user that clickable link so they can reopen the page if they lose the tab.

4. **Act on the verdict.** When the user clicks, the command exits and re-invokes you with one
   stdout line:
   - `MDVIEW_VERDICT {"verdict":"approve"}` → proceed.
   - `MDVIEW_VERDICT {"verdict":"changes","comment":"…"}` → read the comment, make the changes,
     and (if useful) re-render for another round.
   - `MDVIEW_VERDICT {"verdict":"dismissed"}` → the user closed the tab / didn't decide; ask
     how they'd like to proceed.
````

- [ ] **Step 4: Write `commands/mdview.md`**

````markdown
---
description: Open a markdown file in the browser with Approve / Request-changes buttons and wait for the verdict.
argument-hint: <path-to-file.md>
---

Use the `mdview-review` skill to render `$ARGUMENTS` for review. Run the cached binary
backgrounded, surface the `http://127.0.0.1:PORT/` URL, and act on the `MDVIEW_VERDICT` line
(approve → proceed; changes → apply the comment; dismissed → ask how to proceed).
````

- [ ] **Step 5: Validate manifests parse**

Run:
```bash
cd ~/Documents/Develop/mdview-review
python3 -c "import json;[json.load(open(f)) for f in ['.claude-plugin/plugin.json','.claude-plugin/marketplace.json']];print('json ok')"
```
Expected: `json ok`.

- [ ] **Step 6: Commit + push**

```bash
git add -A && git commit -m "feat: plugin + single-plugin marketplace + skill + /mdview command"
git push
```

---

### Task 8: Cut the first release, slim CLAUDE.md, install & verify end-to-end

**Files:**
- Create: `docs/design.md` (move the spec into the repo)
- Modify: `~/.config/claude-subscriptions/configs/gmail/CLAUDE.md` ("Reviewing markdown files")

**Interfaces:**
- Consumes: everything above.
- Produces: a published `v0.1.0` release; the skill installed and verified end-to-end.

- [ ] **Step 1: Move the design doc into the repo**

```bash
cp ~/.claude/scripts/mdview-review-design.md ~/Documents/Develop/mdview-review/docs/design.md
mkdir -p ~/Documents/Develop/mdview-review/docs
cp ~/.claude/scripts/mdview-review-design.md ~/Documents/Develop/mdview-review/docs/design.md
cd ~/Documents/Develop/mdview-review && git add -A && git commit -m "docs: add design spec"
```

- [ ] **Step 2: Tag and push the release; confirm assets**

```bash
cd ~/Documents/Develop/mdview-review
git tag v0.1.0 && git push origin v0.1.0
gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
gh release view v0.1.0 --json assets --jq '.assets[].name'
```
Expected: assets list includes `mdview-darwin-arm64`, `mdview-darwin-amd64`, `mdview-linux-amd64`, `mdview-linux-arm64`, `mdview-windows-amd64.exe`, `SHA256SUMS`.

- [ ] **Step 3: Slim the global CLAUDE.md trigger**

Replace the entire "# Reviewing markdown files" section in
`~/.config/claude-subscriptions/configs/gmail/CLAUDE.md` with:
```markdown
# Reviewing markdown files
When you'd have me review a markdown file (a spec, design doc, plan, etc.), invoke the
`mdview-review` skill instead of pointing me at the path. Run its binary **backgrounded**
(it blocks until I click a button), surface the `http://127.0.0.1:PORT/` URL it prints, and
act on the `MDVIEW_VERDICT` line it emits on exit: `approve` → proceed; `changes` → apply my
comment; `dismissed` → ask how to proceed.
```

- [ ] **Step 4: Install the plugin and verify the bootstrap + a real review**

```bash
rm -rf ~/.cache/mdview-review   # force a clean first-run download
```
In Claude Code:
```
/plugin marketplace add claude-code-tools/mdview-review
/plugin install mdview-review
```
Then ask the agent to review `docs/design.md`. Confirm: the binary downloads + checksum-verifies,
the browser opens the rendered page with the buttons, clicking **Approve** returns
`{"verdict":"approve"}`, and **Request changes** returns the typed comment.

- [ ] **Step 5: Manual browser checklist (one pass)**

- Approve → confirmation "✓ Sent"; session receives `approve`.
- Request changes → textarea; Submit disabled until non-empty; ⌘/Ctrl+Enter submits; Esc cancels.
- Close the tab without deciding → session receives `dismissed`.
- A doc with a ```` ```mermaid ```` block renders the diagram; the bar doesn't clip content.

- [ ] **Step 6: Final commit**

```bash
cd ~/Documents/Develop/mdview-review && git add -A && git commit -m "docs: README usage" --allow-empty && git push
```

---

## Self-Review

**Spec coverage:** localhost server + buttons (T2–T5) · verdict contract (T3) · lifecycle/cleanup/port (T3–T4) · cross-platform binary + embed (T2,T5,T6) · CI/release matrix (T6) · repo-as-marketplace + plugin + skill + command (T7) · triggering via description + CLAUDE.md + slash command (T7,T8) · checksum-verified bootstrap (T7) · mobile explicitly out of scope. ✔

**Placeholder scan:** none — vendored binary assets are downloaded via concrete `curl` commands (not inlineable), every code/test/manifest body is complete.

**Type consistency:** `render.Page(src []byte, token string) (string, error)`, `server.Start(Options) (*Handle, error)`, `Handle.Wait() Verdict`, `Verdict{Verdict, Comment}` are used identically across T2–T5. The `MDVIEW_VERDICT {json}` line, asset names `mdview-<os>-<arch>[.exe]`, and version `v0.1.0` match across server/main/release/skill.
