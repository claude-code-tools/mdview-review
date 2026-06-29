package rendezvous

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestWritePerms(t *testing.T) {
	// Point at a not-yet-existing nested dir so Write's MkdirAll actually creates it
	// with the requested 0700 mode. (t.TempDir() itself is created by Go with a
	// host-dependent mode — 0755 on macOS — so stat-ing it would not verify our spec value.)
	dir := filepath.Join(t.TempDir(), "servers")
	t.Setenv("MDVIEW_STATE_DIR", dir)
	if err := Write(Record{PID: 1, Key: "k", Port: 30000}); err != nil {
		t.Fatal(err)
	}
	p, err := Path("k")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
}
