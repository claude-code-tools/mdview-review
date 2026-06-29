package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	url := "http://127.0.0.1:" + strconv.Itoa(rec.Port) + "/verdict"
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(`{"verdict":"approve"}`))
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
		t.Fatalf("process should exit 0 on approve: %v", err)
	}
	if got, _ := rendezvous.Read(key); got != nil {
		t.Fatalf("rendezvous file should be removed on exit, got %+v", got)
	}
}

// TestReplaceOnReuseReclaimesStickyPort verifies that when a second binary is launched with
// the same MDVIEW_KEY, it kills the first binary and reclaims the deterministic sticky port
// (rather than falling back to an ephemeral port).
func TestReplaceOnReuseReclaimesStickyPort(t *testing.T) {
	state := t.TempDir()
	t.Setenv("MDVIEW_STATE_DIR", state) // so this process's rendezvous.Read sees the same dir
	bin := buildBin(t)

	md := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(md, []byte("# replace-on-reuse test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const key = "itest-replace-key"
	stickyPort := rendezvous.PortForKey(key)

	// ---- Round 1: start binary, wait for rendezvous record to appear.
	cmd1 := exec.Command(bin, md)
	cmd1.Env = append(os.Environ(),
		"MDVIEW_KEY="+key,
		"MDVIEW_STATE_DIR="+state,
		"MDVIEW_BROWSER=true",      // no real browser
		"MDVIEW_NO_CLIENT_SECONDS=60", // stay alive — we don't post a verdict
	)
	if err := cmd1.Start(); err != nil {
		t.Fatal(err)
	}

	var rec1 *rendezvous.Record
	deadline1 := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline1) {
		rec1, _ = rendezvous.Read(key)
		if rec1 != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rec1 == nil {
		_ = cmd1.Process.Kill()
		t.Fatal("round-1: rendezvous file never appeared")
	}
	if rec1.Port != stickyPort {
		_ = cmd1.Process.Kill()
		t.Fatalf("round-1: port %d != sticky %d", rec1.Port, stickyPort)
	}
	round1PID := cmd1.Process.Pid

	// ---- Round 2: launch with the SAME key — should kill round 1 and reclaim the port.
	cmd2 := exec.Command(bin, md)
	cmd2.Env = append(os.Environ(),
		"MDVIEW_KEY="+key,
		"MDVIEW_STATE_DIR="+state,
		"MDVIEW_BROWSER=true",
		"MDVIEW_NO_CLIENT_SECONDS=60",
	)
	if err := cmd2.Start(); err != nil {
		_ = cmd1.Process.Kill()
		t.Fatal(err)
	}

	// Poll until the rendezvous record belongs to round-2's process.
	var rec2 *rendezvous.Record
	deadline2 := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline2) {
		rec2, _ = rendezvous.Read(key)
		if rec2 != nil && rec2.PID == cmd2.Process.Pid {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rec2 == nil || rec2.PID != cmd2.Process.Pid {
		_ = cmd1.Process.Kill()
		_ = cmd2.Process.Kill()
		t.Fatalf("round-2: rendezvous record never updated to round-2 pid %d (got %v)", cmd2.Process.Pid, rec2)
	}

	// The critical assertion: round 2 must have reclaimed the sticky port, not fallen back
	// to an ephemeral port.
	if rec2.Port != stickyPort {
		_ = cmd1.Process.Kill()
		_ = cmd2.Process.Kill()
		t.Fatalf("round-2 got ephemeral port %d instead of sticky port %d — sticky-port reclaim broken", rec2.Port, stickyPort)
	}

	// Reap round 1 (it was SIGTERM'd by round 2, so exits non-zero — just drain it).
	_ = cmd1.Wait()

	// Confirm round 1 is no longer alive.
	pollDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(pollDeadline) {
		if !rendezvous.Alive(round1PID) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rendezvous.Alive(round1PID) {
		_ = cmd2.Process.Kill()
		t.Fatalf("round-1 pid %d should be dead after round-2 replaced it", round1PID)
	}

	// Clean up round 2: POST an approve verdict.
	verdictURL := fmt.Sprintf("http://127.0.0.1:%s/verdict", strconv.Itoa(rec2.Port))
	req, _ := http.NewRequest("POST", verdictURL, bytes.NewBufferString(`{"verdict":"approve"}`))
	req.Header.Set("Authorization", "Bearer "+rec2.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = cmd2.Process.Kill()
		t.Fatalf("POST /verdict to round-2: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		resp.Body.Close()
		_ = cmd2.Process.Kill()
		t.Fatalf("POST /verdict returned %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	if err := cmd2.Wait(); err != nil {
		t.Fatalf("round-2 process should exit 0 on approve: %v", err)
	}
}
