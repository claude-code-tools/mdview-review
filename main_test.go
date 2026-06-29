package main

import (
	"bytes"
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
