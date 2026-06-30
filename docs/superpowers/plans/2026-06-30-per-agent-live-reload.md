# Per-Agent Live-Reload (Sticky Tab) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reuse one browser tab **per agent** across a review changes-cycle, reloading the doc at each round, with zero lingering processes.

**Architecture:** Keep the binary process-per-round (exits on every verdict → push preserved). Add a per-agent **sticky port** (derived from a key) so the open tab reconnects to the next round's server and reloads; a per-key **rendezvous file** for definitive `--stop` and replace-on-reuse; an **owner-pid watch** + lowered max-lifetime as teardown floors.

**Tech Stack:** Go 1.22, stdlib only (`hash/fnv`, `encoding/json`, `syscall`, `os`, `net/http`). goldmark already vendored. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-06-30-per-agent-live-reload-design.md`

## Global Constraints

- **No new dependencies** — stdlib only.
- **Must compile for windows/amd64** (a release target) — POSIX-only primitives go behind `//go:build` tags with Windows variants.
- **Push-not-poll preserved** — the process still exits on every verdict; `MDVIEW_VERDICT {…}` last-stdout-line contract and exit-0-for-any-outcome are unchanged.
- **Opt-in / backward compatible** — with `MDVIEW_KEY` unset, behavior is identical to today (random ephemeral port, no rendezvous file).
- **Sticky-port range: 20000–39999.**
- **Max-lifetime default: 2h** (was 6h). Still overridable via `MDVIEW_MAX_LIFETIME_SECONDS`.
- **Owner-pid watch & orphan-reap are POSIX-only**; Windows leans on no-client / tab-close / max-lifetime.
- **Embedded-asset rule:** editing `internal/render/assets/*` requires rebuilding the binary for `./mdview` to change (tests read the embedded copies, so `go test` sees edits without a rebuild).
- **Run server-touching tests under `-race`.**
- **State dir** is `~/.cache/mdview-review/servers/` (per `os.UserCacheDir()`), overridable via `MDVIEW_STATE_DIR` (for hermetic tests).

---

### Task 1: `rendezvous` package — keying, file I/O, stop

**Files:**
- Create: `internal/rendezvous/rendezvous.go`
- Create: `internal/rendezvous/proc_unix.go`
- Create: `internal/rendezvous/proc_windows.go`
- Test: `internal/rendezvous/rendezvous_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces:
  - `type Record struct { PID int; Port int; Token string; Key string; StartedAt int64 }` (JSON tags `pid,port,token,key,startedAt`)
  - `func PortForKey(key string) int` — deterministic, in [20000, 39999]
  - `func Path(key string) (string, error)`
  - `func Write(rec Record) error`
  - `func Read(key string) (*Record, error)` — `(nil, nil)` if absent
  - `func Remove(key string) error` — no error if absent
  - `func RemoveIfOwner(key string, pid int) error` — removes only if the on-disk PID matches
  - `func Stop(key string) error` — SIGTERM a live recorded server, then remove; idempotent
  - `func Alive(pid int) bool` — POSIX signal-0 probe (Windows: best-effort via `os.FindProcess`)

- [ ] **Step 1: Write the failing tests**

```go
// internal/rendezvous/rendezvous_test.go
package rendezvous

import (
	"os"
	"os/exec"
	"testing"
)

func TestPortForKeyDeterministicInRange(t *testing.T) {
	a := PortForKey("agent-1")
	b := PortForKey("agent-1")
	c := PortForKey("agent-2")
	if a != b {
		t.Fatalf("not deterministic: %d != %d", a, b)
	}
	if a == c {
		t.Fatalf("distinct keys collided: %d", a)
	}
	for _, p := range []int{a, b, c} {
		if p < 20000 || p > 39999 {
			t.Fatalf("port %d out of range", p)
		}
	}
}

func TestWriteReadRemove(t *testing.T) {
	t.Setenv("MDVIEW_STATE_DIR", t.TempDir())
	rec := Record{PID: 4321, Port: 25000, Token: "tok", Key: "k", StartedAt: 1}
	if err := Write(rec); err != nil {
		t.Fatal(err)
	}
	got, err := Read("k")
	if err != nil || got == nil {
		t.Fatalf("read: %v %v", got, err)
	}
	if got.PID != 4321 || got.Port != 25000 || got.Token != "tok" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if err := Remove("k"); err != nil {
		t.Fatal(err)
	}
	got, _ = Read("k")
	if got != nil {
		t.Fatalf("expected nil after remove, got %+v", got)
	}
}

func TestReadAbsentIsNil(t *testing.T) {
	t.Setenv("MDVIEW_STATE_DIR", t.TempDir())
	got, err := Read("missing")
	if err != nil || got != nil {
		t.Fatalf("want (nil,nil), got (%v,%v)", got, err)
	}
}

func TestRemoveIfOwner(t *testing.T) {
	t.Setenv("MDVIEW_STATE_DIR", t.TempDir())
	_ = Write(Record{PID: 100, Key: "k"})
	if err := RemoveIfOwner("k", 999); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read("k"); got == nil {
		t.Fatal("non-owner must not remove")
	}
	if err := RemoveIfOwner("k", 100); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read("k"); got != nil {
		t.Fatal("owner should have removed")
	}
}

func TestStopDeadPidIsNoOpAndCleansFile(t *testing.T) {
	t.Setenv("MDVIEW_STATE_DIR", t.TempDir())
	// PID 1 is alive but not ours to signal cleanly; use an obviously-dead pid instead.
	_ = Write(Record{PID: 2147480000, Key: "k", Port: 30000})
	if err := Stop("k"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read("k"); got != nil {
		t.Fatal("Stop must remove the stale file")
	}
	// Stop with no record is also a no-op.
	if err := Stop("k"); err != nil {
		t.Fatal(err)
	}
}

func TestStopTerminatesLiveProcess(t *testing.T) {
	t.Setenv("MDVIEW_STATE_DIR", t.TempDir())
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	_ = Write(Record{PID: pid, Key: "k", Port: 30001})
	if !Alive(pid) {
		t.Fatal("spawned process should be alive")
	}
	if err := Stop("k"); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // reap; SIGTERM kills `sleep`
	if Alive(pid) {
		t.Fatal("Stop should have terminated the process")
	}
}

func TestAliveSelf(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("self should be alive")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/gunwoo/Documents/Develop/mdview-review && go test ./internal/rendezvous/`
Expected: FAIL — `undefined: PortForKey` (package has no implementation yet).

- [ ] **Step 3: Write the cross-platform core**

```go
// internal/rendezvous/rendezvous.go
package rendezvous

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
)

// Record is the on-disk handoff describing a running server for a key.
type Record struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	Token     string `json:"token"`
	Key       string `json:"key"`
	StartedAt int64  `json:"startedAt"`
}

func keyHash(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

// PortForKey maps key deterministically into [20000, 39999] — above the well-known range and
// below the typical macOS ephemeral range, to minimize clashes with OS-assigned ports.
func PortForKey(key string) int {
	return 20000 + int(keyHash(key)%20000)
}

func dir() (string, error) {
	if d := os.Getenv("MDVIEW_STATE_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mdview-review", "servers"), nil
}

// Path returns the rendezvous file path for key.
func Path(key string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, fmt.Sprintf("%08x.json", keyHash(key))), nil
}

// Write persists rec for its key (0600 file, 0700 dir).
func Write(rec Record) error {
	d, err := dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	p, err := Path(rec.Key)
	if err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// Read loads the record for key. Returns (nil, nil) when absent.
func Read(key string) (*Record, error) {
	p, err := Path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Remove deletes the rendezvous file for key (no error if absent).
func Remove(key string) error {
	p, err := Path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveIfOwner removes the file only when the recorded PID matches pid, so a dying stale
// server cannot delete a newer server's record.
func RemoveIfOwner(key string, pid int) error {
	rec, err := Read(key)
	if err != nil || rec == nil {
		return err
	}
	if rec.PID == pid {
		return Remove(key)
	}
	return nil
}

// Stop terminates any live server recorded for key, then removes the file. Idempotent: a
// missing record or a dead PID just cleans up the stale file.
func Stop(key string) error {
	rec, err := Read(key)
	if err != nil || rec == nil {
		return err
	}
	if rec.PID > 0 && Alive(rec.PID) {
		terminate(rec.PID)
	}
	return Remove(key)
}
```

- [ ] **Step 4: Write the platform primitives**

```go
// internal/rendezvous/proc_unix.go
//go:build !windows

package rendezvous

import "syscall"

// Alive reports whether pid is a live process via a signal-0 probe.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func terminate(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }
```

```go
// internal/rendezvous/proc_windows.go
//go:build windows

package rendezvous

import "os"

// Alive is best-effort on Windows (owner-watch/replace are POSIX-first; Windows relies on
// no-client / max-lifetime). FindProcess fails for a non-existent pid.
func Alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}

func terminate(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/rendezvous/`
Expected: PASS (all tests).

- [ ] **Step 6: Commit**

```bash
git add internal/rendezvous/
git commit -m "feat(rendezvous): per-key port derivation, file I/O, and stop"
```

---

### Task 2: server — instance nonce + SSE `hello` event

**Files:**
- Modify: `internal/server/server.go` (Options, Handle, `Start`, `handleEvents`)
- Test: `internal/server/server_test.go` (add a test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Options.Nonce string`; when set, `GET /events` emits `event: hello\ndata: <nonce>\n\n` right after the `: connected` comment. Stored on `Handle.nonce`.

- [ ] **Step 1: Write the failing test**

```go
// add to internal/server/server_test.go
func TestEventsEmitsHelloNonce(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t", Nonce: "nonce-123",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour})
	t.Cleanup(func() { h.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.URL+"events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "event: hello") || !strings.Contains(got, "nonce-123") {
		t.Fatalf("missing hello nonce in stream: %q", got)
	}
}
```

Add `"strings"` to the test file imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestEventsEmitsHelloNonce`
Expected: FAIL — `unknown field 'Nonce' in struct literal`.

- [ ] **Step 3: Implement the nonce**

In `internal/server/server.go`, add the field to `Options`:

```go
	TabCloseGrace   time.Duration // grace before treating all-clients-gone as a tab close (default 1s)
	Nonce           string        // instance id sent over SSE so a reconnecting tab can detect a new server and reload
```

Add to `Handle`:

```go
	token string
	page  string
	nonce string
	grace time.Duration
```

In `Start`, set it on the handle (alongside `token: o.Token`):

```go
		token:  o.Token,
		page:   o.Page,
		nonce:  o.Nonce,
		grace:  o.TabCloseGrace,
```

In `handleEvents`, after the existing `: connected` write and before `fl.Flush()`:

```go
	io.WriteString(w, ": connected\n\n")
	if h.nonce != "" {
		io.WriteString(w, "event: hello\ndata: "+h.nonce+"\n\n")
	}
	fl.Flush()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -race`
Expected: PASS (new test + all existing).

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): emit instance nonce as an SSE hello event"
```

---

### Task 3: server — owner-pid watch in the lifecycle

**Files:**
- Modify: `internal/server/server.go` (`Options`, `lifecycle`)
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Options.OwnerAlive func() bool`. When non-nil, the lifecycle polls it on the existing ppid ticker; a `false` result resolves `dismissed`. (main.go supplies `func() bool { return rendezvous.Alive(ownerPID) }`.)

- [ ] **Step 1: Write the failing test**

```go
// add to internal/server/server_test.go
func TestOwnerAliveWatch(t *testing.T) {
	ownerUp := make(chan struct{})
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour, PPIDPoll: 10 * time.Millisecond,
		OwnerAlive: func() bool {
			select {
			case <-ownerUp:
				return false // owner "died"
			default:
				return true
			}
		}})
	t.Cleanup(func() { h.Close() })
	close(ownerUp) // signal the owner is gone
	if v := h.Wait(); v.Verdict != "dismissed" {
		t.Fatalf("got %+v, want dismissed", v)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestOwnerAliveWatch`
Expected: FAIL — `unknown field 'OwnerAlive'`.

- [ ] **Step 3: Implement the watch**

Add to `Options`:

```go
	Nonce           string         // instance id sent over SSE so a reconnecting tab can detect a new server and reload
	OwnerAlive      func() bool    // optional: when it returns false, resolve dismissed (session/owner died)
```

In `lifecycle`, extend the `ppid.C` case:

```go
		case <-ppid.C:
			if orphaned(os.Getppid()) {
				h.decide(Verdict{Verdict: "dismissed"})
			}
			if o.OwnerAlive != nil && !o.OwnerAlive() {
				h.decide(Verdict{Verdict: "dismissed"})
			}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): owner-alive watch resolves dismissed when the session dies"
```

---

### Task 4: server — sticky-port bind with ephemeral fallback

**Files:**
- Modify: `internal/server/server.go` (`Options`, `Start`, new `listen` helper)
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Options.StickyPort int`. `Start` binds `127.0.0.1:<StickyPort>` when `> 0`; on failure (or `0`) it falls back to `127.0.0.1:0` (random). `Handle.Port`/`Handle.URL` reflect whatever was actually bound.

- [ ] **Step 1: Write the failing tests**

```go
// add to internal/server/server_test.go
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func TestStickyPortUsed(t *testing.T) {
	want := freePort(t)
	h, err := Start(Options{Page: "p", Token: "t", StickyPort: want,
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if h.Port != want {
		t.Fatalf("got port %d, want sticky %d", h.Port, want)
	}
}

func TestStickyPortFallsBackWhenTaken(t *testing.T) {
	taken := freePort(t)
	blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", taken))
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	h, err := Start(Options{Page: "p", Token: "t", StickyPort: taken,
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if h.Port == taken || h.Port == 0 {
		t.Fatalf("expected fallback to a different port, got %d", h.Port)
	}
}
```

Add `"fmt"` and `"net"` to the test file imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run TestStickyPort`
Expected: FAIL — `unknown field 'StickyPort'`.

- [ ] **Step 3: Implement sticky bind**

Add to `Options`:

```go
	OwnerAlive      func() bool    // optional: when it returns false, resolve dismissed (session/owner died)
	StickyPort      int            // preferred port; 0 or unavailable -> random ephemeral
```

Replace the listen block at the top of `Start`:

```go
	ln, err := listen(o.StickyPort)
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
```

Add the helper near the bottom of the file:

```go
// listen prefers stickyPort, falling back to a random ephemeral loopback port (the original
// collision-free behavior) when stickyPort is 0 or already taken.
func listen(stickyPort int) (net.Listener, error) {
	if stickyPort > 0 {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", stickyPort)); err == nil {
			return ln, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}
```

Add `"fmt"` to `server.go` imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): sticky-port bind with ephemeral fallback"
```

---

### Task 5: render — `review.js` reload-on-reconnect

**Files:**
- Modify: `internal/render/assets/review.js`
- Test: `internal/render/render_test.go` (assert the asset is wired into the page)

**Interfaces:**
- Consumes: the SSE `hello` event from Task 2.
- Produces: the served review page now reloads when its `/events` connection reattaches to a **new** server instance (different nonce).

- [ ] **Step 1: Write the failing test**

```go
// add to internal/render/render_test.go
func TestPageWiresReloadOnReconnect(t *testing.T) {
	page, err := Page([]byte("# hi"), "tok")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`addEventListener("hello"`, "location.reload()"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}
```

Ensure `"strings"` is imported in `render_test.go` (add if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -run TestPageWiresReloadOnReconnect`
Expected: FAIL — the substrings are not present yet.

- [ ] **Step 3: Implement the client logic**

Replace the keep-alive line in `internal/render/assets/review.js`:

```js
  // Keep-alive so the server can detect a closed tab.
  try { new EventSource("/events"); } catch (e) {}
```

with:

```js
  // Keep-alive (so the server can detect a closed tab) + reload-on-reconnect: if the SSE
  // reattaches to a NEW server instance (different nonce), the doc changed across a review
  // round — reload to pick up the fresh page and token.
  var seenNonce = null;
  try {
    var es = new EventSource("/events");
    es.addEventListener("hello", function (e) {
      if (seenNonce === null) seenNonce = e.data;
      else if (e.data !== seenNonce) location.reload();
    });
  } catch (e) {}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/render/`
Expected: PASS.

- [ ] **Step 5: Rebuild the binary (embedded-asset rule) and sanity check**

Run:
```bash
go build -o mdview . && ./mdview --print assets/demo.md | grep -c 'location.reload()'
```
Expected: prints `1` (the asset is baked into the rendered page).

- [ ] **Step 6: Commit**

```bash
git add internal/render/assets/review.js internal/render/render_test.go
git commit -m "feat(render): reload the review tab when it reconnects to a new server instance"
```

---

### Task 6: main — wire key/owner/sticky + `--stop` + lower max-lifetime

**Files:**
- Modify: `main.go`
- Test: `main_test.go` (create — integration test that builds and runs the binary)

**Interfaces:**
- Consumes: `render.Page`, `server.Start` (Options: `Nonce`, `OwnerAlive`, `StickyPort`), all of `rendezvous`.
- Produces: review mode honors `MDVIEW_KEY` (sticky port + rendezvous file + replace-on-reuse) and `MDVIEW_OWNER_PID` (owner watch); a new `mdview --stop` mode; max-lifetime default 2h.

- [ ] **Step 1: Write the failing integration test**

```go
// main_test.go
package main

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/claude-code-tools/mdview-review/internal/rendezvous"
)

func buildBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mdview")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func TestStopNoServerIsZeroExit(t *testing.T) {
	state := t.TempDir()
	bin := buildBin(t)
	cmd := exec.Command(bin, "--stop")
	cmd.Env = append(os.Environ(), "MDVIEW_KEY=nobody", "MDVIEW_STATE_DIR="+state)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("--stop with no server should exit 0: %v\n%s", err, out)
	}
}

func TestReviewKeyLifecycle(t *testing.T) {
	state := t.TempDir()
	t.Setenv("MDVIEW_STATE_DIR", state) // so this process's rendezvous.Read sees the same dir
	bin := buildBin(t)

	md := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(md, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const key = "itest-key"

	cmd := exec.Command(bin, md)
	cmd.Env = append(os.Environ(),
		"MDVIEW_KEY="+key,
		"MDVIEW_STATE_DIR="+state,
		"MDVIEW_BROWSER=true", // no real browser
		"MDVIEW_NO_CLIENT_SECONDS=30",
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Wait for the rendezvous file to appear.
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
	if rec.Port != rendezvous.PortForKey(key) {
		t.Fatalf("port %d != sticky %d", rec.Port, rendezvous.PortForKey(key))
	}

	// POST an approve verdict using the recorded token+port.
	url := "http://127.0.0.1:" + itoa(rec.Port) + "/verdict"
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(`{"verdict":"approve"}`))
	req.Header.Set("Authorization", "Bearer "+rec.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("process should exit 0 on approve: %v", err)
	}
	if got, _ := rendezvous.Read(key); got != nil {
		t.Fatalf("rendezvous file should be removed on exit, got %+v", got)
	}
}

func itoa(n int) string {
	return string([]byte(strconvItoa(n)))
}

func strconvItoa(n int) string { // tiny local helper to avoid an extra import in the example
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
```

> Note for the implementer: replace the `itoa`/`strconvItoa` helpers with `strconv.Itoa` and add `"strconv"` to the imports — they're spelled out here only to keep the example self-contained. Use `strconv.Itoa(rec.Port)`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestStopNoServer|TestReviewKeyLifecycle'`
Expected: FAIL — `--stop` is unknown (binary prints usage / non-zero) and no rendezvous file is written.

- [ ] **Step 3: Add the `--stop` mode and env parsing to `main`**

In `main.go`, extend the flag switch (add a `--stop` case) and handle it before the file is required:

```go
	args := os.Args[1:]
	mode := "review"
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			fmt.Println("mdview " + version)
			return
		case "--stop":
			stopForKey()
			return
		case "--print":
			mode, args = "print", args[1:]
		case "--view":
			mode, args = "view", args[1:]
		}
	}
```

Add the helper (anywhere below `main`):

```go
// stopForKey definitively tears down this agent's preview server (if any) for MDVIEW_KEY.
// Idempotent: no key or no server is a no-op, exit 0.
func stopForKey() {
	key := os.Getenv("MDVIEW_KEY")
	if key == "" {
		return
	}
	if err := rendezvous.Stop(key); err != nil {
		fmt.Fprintf(os.Stderr, "mdview: stop: %v\n", err)
	}
}
```

Update the `usage` line to mention `--stop`:

```go
	fmt.Fprintln(os.Stderr, "usage: mdview [--view | --print | --stop | --version] <file.md>")
```

Add the import:

```go
	"github.com/claude-code-tools/mdview-review/internal/rendezvous"
```

- [ ] **Step 4: Wire sticky port, rendezvous, owner-watch, and 2h default into review mode**

Replace the review-mode block (everything from `// Default: review mode` to `os.Exit(0)`) with:

```go
	// Default: review mode — render with the dock, serve, block until the user decides.
	token := newToken()
	nonce := newToken()
	page, err := render.Page(src, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: render: %v\n", err)
		os.Exit(1)
	}

	key := os.Getenv("MDVIEW_KEY")
	stickyPort := 0
	if key != "" {
		// Replace-on-reuse: tear down any stale server holding this key, then claim its port.
		_ = rendezvous.Stop(key)
		stickyPort = rendezvous.PortForKey(key)
	}

	opts := server.Options{
		Page:            page,
		Token:           token,
		Nonce:           nonce,
		StickyPort:      stickyPort,
		NoClientTimeout: envDur("MDVIEW_NO_CLIENT_SECONDS", 60),
		MaxLifetime:     envDur("MDVIEW_MAX_LIFETIME_SECONDS", 2*3600),
	}
	if ownerPID := envInt("MDVIEW_OWNER_PID"); ownerPID > 0 {
		opts.OwnerAlive = func() bool { return rendezvous.Alive(ownerPID) }
	}

	h, err := server.Start(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}

	if key != "" {
		_ = rendezvous.Write(rendezvous.Record{
			PID: os.Getpid(), Port: h.Port, Token: token, Key: key, StartedAt: time.Now().Unix(),
		})
	}

	fmt.Fprintf(os.Stderr, "mdview: review server at %s\n", h.URL)
	openBrowser(h.URL)

	v := h.Wait()
	if key != "" {
		// os.Exit skips defers — remove explicitly, but only if we still own the record.
		_ = rendezvous.RemoveIfOwner(key, os.Getpid())
	}
	out, _ := json.Marshal(v)
	fmt.Printf("MDVIEW_VERDICT %s\n", out)
	os.Exit(0)
```

Add the `envInt` helper near `envDur`:

```go
func envInt(name string) int {
	if s := os.Getenv(name); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}
```

(`strconv` is already imported.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test . -race && go build -o mdview .`
Expected: PASS; binary builds.

- [ ] **Step 6: Cross-compile check (Windows must still build)**

Run: `GOOS=windows GOARCH=amd64 go build -o /dev/null .`
Expected: no output, exit 0 (the build-tagged `proc_windows.go` compiles).

- [ ] **Step 7: Commit**

```bash
git add main.go main_test.go
git commit -m "feat(main): per-agent sticky tab, --stop, owner-pid watch, 2h max-lifetime"
```

---

### Task 7: docs — SKILL, CLAUDE, README

**Files:**
- Modify: `skills/mdview-review/SKILL.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the finished behavior from Tasks 1–6.
- Produces: documented usage of `MDVIEW_KEY`, `MDVIEW_OWNER_PID`, `--stop`, and the per-agent persistent-tab behavior.

> The `VER=vX.Y.Z` pins in SKILL.md are bumped by `scripts/release.sh` at release time — **do not hand-edit them here.** This task only adds the new env/flag guidance and prose.

- [ ] **Step 1: Update SKILL.md run guidance**

In `skills/mdview-review/SKILL.md`, in the run step, set the per-agent key + owner pid on the review invocation and document the persistent tab. Use this wording (adjust to fit the surrounding steps):

```markdown
Run the cached binary on the file. Set two env vars so your review reuses **one tab across
rounds** and tears down cleanly:

- `MDVIEW_KEY` — a stable id unique to **you** (main session: your session id; a subagent: its
  own task id). The same key reuses the same browser tab across review rounds.
- `MDVIEW_OWNER_PID="$PPID"` — lets the server exit if this session dies.

    MDVIEW_KEY="<your-stable-id>" MDVIEW_OWNER_PID="$PPID" \
      $HOME/.cache/mdview-review/<VER>/mdview <path-to-file.md>

- **Main session:** run it **backgrounded** (re-invoked on the user's click).
- **Subagent:** run it **blocking** with a long timeout, and definitively tear your preview
  down at end-of-task with:

      MDVIEW_KEY="<your-stable-id>" $HOME/.cache/mdview-review/<VER>/mdview --stop
```

- [ ] **Step 2: Update CLAUDE.md architecture notes**

In `CLAUDE.md`, under Architecture / Distribution, add a short paragraph:

```markdown
**Per-agent sticky tab (live-reload).** With `MDVIEW_KEY` set, the server binds a deterministic
port (`internal/rendezvous.PortForKey`, range 20000–39999) instead of a random one, records a
per-key rendezvous file (`~/.cache/mdview-review/servers/`, overridable via `MDVIEW_STATE_DIR`),
and emits an SSE instance nonce so a reconnecting tab reloads when a new round's server claims
the same port. It stays process-per-round (exits on every verdict), so there is no daemon.
Teardown floors: `--stop` (per key), tab-close/no-client, orphan-reap (`ppid==1`),
`MDVIEW_OWNER_PID` watch, and a 2h max-lifetime. All opt-in: with no `MDVIEW_KEY`, behavior is
unchanged (random port, no rendezvous file).
```

- [ ] **Step 3: Update README.md env table**

In `README.md`, under the environment-overrides paragraph, add:

```markdown
For agent integrations: `MDVIEW_KEY` enables a persistent per-key browser tab across review
rounds; `MDVIEW_OWNER_PID` makes the server exit when that pid dies; `mdview --stop` (with the
same `MDVIEW_KEY`) definitively tears the server down. `MDVIEW_STATE_DIR` overrides where the
per-key rendezvous files live.
```

- [ ] **Step 4: Verify docs build/run references are accurate**

Run: `go build -o mdview . && MDVIEW_KEY=doc ./mdview --stop && echo OK`
Expected: prints `OK` (the documented `--stop` works as written).

- [ ] **Step 5: Commit**

```bash
git add skills/mdview-review/SKILL.md CLAUDE.md README.md
git commit -m "docs: document MDVIEW_KEY/OWNER_PID/--stop and the per-agent sticky tab"
```

---

## After all tasks

- Run the full suite once more: `go test ./... -race` (expected: all PASS) and
  `go vet ./...` (expected: clean).
- Manual end-to-end (the behavior unit tests can't cover): run
  `MDVIEW_KEY=manual MDVIEW_OWNER_PID=$PPID ./mdview assets/demo.md` in one terminal, click
  **Request changes**, edit `assets/demo.md`, re-run the same command — confirm the **same
  browser tab** reloads to the edited doc rather than opening a new tab.
- **Release** (separate from this plan): once merged, cut a release with
  `scripts/release.sh X.Y.Z` — it bumps `plugin.json` + the `VER` pins in SKILL.md, tags, and
  triggers the workflow. The new binary must ship before the bumped SKILL pin points at it.

## Self-Review

**Spec coverage:**
- Port derivation + fallback → Task 1 (`PortForKey`) + Task 4 (`listen`). ✓
- Rendezvous file (pid/port/token/key, 0600) → Task 1. ✓
- Replace-on-reuse → Task 6 (`rendezvous.Stop` before bind). ✓
- Reload-on-reconnect (nonce + client reload) → Task 2 + Task 5. ✓
- `--stop` → Task 6. ✓
- Teardown: owner-pid watch → Task 3 + Task 6; orphan-reap → unchanged; max-life 2h → Task 6; tab-close/no-client → unchanged. ✓
- Subagent guidance / `MDVIEW_KEY`/`MDVIEW_OWNER_PID` → Task 7. ✓
- Backward compatible (no key) → Task 4 (`StickyPort 0` → random) + Task 6 (all key logic gated on `key != ""`). ✓
- Windows compiles → Task 1 (build tags) + Task 6 Step 6 (cross-build check). ✓

**Placeholder scan:** none — every code step shows complete code. (The `itoa` example in Task 6 is explicitly flagged to be replaced with `strconv.Itoa`.)

**Type consistency:** `Options` field names (`Nonce`, `OwnerAlive`, `StickyPort`) are introduced once and reused identically in main.go; `rendezvous.Record` fields match between Write (Task 6) and Read/tests (Task 1); `Alive`/`Stop`/`PortForKey`/`RemoveIfOwner` signatures match their call sites.
