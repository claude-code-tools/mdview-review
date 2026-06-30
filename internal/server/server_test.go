package server

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startTest(t *testing.T) *Handle {
	t.Helper()
	h, err := Start(Options{
		Page:  `<html><body><div id="mdview-bar"></div>tok</body></html>`,
		Token: "tok", NoClientTimeout: time.Hour, MaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func post(t *testing.T, url, auth, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestServesPageAnd404(t *testing.T) {
	h := startTest(t)
	resp, err := http.Get(h.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	resp2, _ := http.Get(h.URL + "nope")
	if resp2.StatusCode != 404 {
		t.Fatalf("GET /nope = %d", resp2.StatusCode)
	}
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
	if v.Verdict != "approve" {
		t.Fatalf("got %+v", v)
	}
}

func TestChangesRequiresComment(t *testing.T) {
	h := startTest(t)
	if got := post(t, h.URL+"verdict", "tok", `{"verdict":"changes","comment":"  "}`); got != 400 {
		t.Fatalf("empty comment = %d, want 400", got)
	}
	go func() { post(t, h.URL+"verdict", "tok", `{"verdict":"changes","comment":"fix title"}`) }()
	v := h.Wait()
	if v.Verdict != "changes" || v.Comment != "fix title" {
		t.Fatalf("got %+v", v)
	}
}

func TestInvalidVerdict(t *testing.T) {
	h := startTest(t)
	if got := post(t, h.URL+"verdict", "tok", `{"verdict":"nope"}`); got != 400 {
		t.Fatalf("invalid = %d, want 400", got)
	}
}

func TestNoClientBackstop(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: 50 * time.Millisecond, MaxLifetime: time.Hour})
	t.Cleanup(func() { h.Close() })
	if v := h.Wait(); v.Verdict != "dismissed" {
		t.Fatalf("got %+v", v)
	}
}

func TestMaxLifetime(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: time.Hour, MaxLifetime: 50 * time.Millisecond})
	t.Cleanup(func() { h.Close() })
	if v := h.Wait(); v.Verdict != "dismissed" {
		t.Fatalf("got %+v", v)
	}
}

func TestTabClose(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour, TabCloseGrace: 30 * time.Millisecond})
	t.Cleanup(func() { h.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", h.URL+"events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("events = %d", resp.StatusCode)
	}
	cancel() // simulate tab close
	if v := h.Wait(); v.Verdict != "dismissed" {
		t.Fatalf("got %+v", v)
	}
}

// Guards the flush fix: with a live SSE client attached, a POSTed verdict must still receive
// its 204 and Wait must return — exercising the most concurrent path (client + decide).
func TestVerdictReachesClientWithLiveSSE(t *testing.T) {
	h, _ := Start(Options{Page: "p", Token: "tok",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour, TabCloseGrace: time.Hour})
	t.Cleanup(func() { h.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.URL+"events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("events = %d", resp.StatusCode)
	}

	if got := post(t, h.URL+"verdict", "tok", `{"verdict":"approve"}`); got != 204 {
		t.Fatalf("verdict with live SSE = %d, want 204", got)
	}
	if v := h.Wait(); v.Verdict != "approve" {
		t.Fatalf("got %+v", v)
	}
}

func TestOrphanedPredicate(t *testing.T) {
	if !orphaned(1) {
		t.Error("ppid 1 should be orphaned")
	}
	if orphaned(1234) {
		t.Error("ppid 1234 should not be orphaned")
	}
}

func TestEventsEmitsHelloNonce(t *testing.T) {
	h, err := Start(Options{Page: "p", Token: "t", Nonce: "nonce-123",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
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

func TestOwnerAliveWatch(t *testing.T) {
	ownerUp := make(chan struct{})
	h, err := Start(Options{Page: "p", Token: "t",
		NoClientTimeout: time.Hour, MaxLifetime: time.Hour, PPIDPoll: 10 * time.Millisecond,
		OwnerAlive: func() bool {
			select {
			case <-ownerUp:
				return false // owner "died"
			default:
				return true
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	close(ownerUp) // signal the owner is gone
	if v := h.Wait(); v.Verdict != "dismissed" {
		t.Fatalf("got %+v, want dismissed", v)
	}
}

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

func TestFirstClientSignalsAndRetryHint(t *testing.T) {
	h := startTest(t)

	// Open before any client connected.
	select {
	case <-h.FirstClient():
		t.Fatal("FirstClient closed before any client connected")
	default:
	}

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
	if !strings.Contains(string(buf[:n]), "retry:") {
		t.Fatalf("events stream missing reconnect retry hint: %q", string(buf[:n]))
	}

	select {
	case <-h.FirstClient():
	case <-time.After(2 * time.Second):
		t.Fatal("FirstClient not closed after a client connected")
	}
}
