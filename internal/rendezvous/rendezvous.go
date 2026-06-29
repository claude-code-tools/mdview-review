package rendezvous

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"time"
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
//
// When a live PID is found, Stop waits for the sticky port to become free before removing
// the record, so that the successor binary can bind it rather than falling back to an
// ephemeral port.
func Stop(key string) error {
	rec, err := Read(key)
	if err != nil || rec == nil {
		return Remove(key)
	}
	if rec.PID > 0 && Alive(rec.PID) {
		terminate(rec.PID)
		waitPortFree(rec.Port, time.Second)
	}
	return Remove(key)
}

// waitPortFree blocks until 127.0.0.1:port is bindable again or timeout elapses (best-effort:
// a predecessor we SIGTERM'd releases its listener asynchronously). On timeout it returns and
// the caller's ephemeral fallback is the floor.
func waitPortFree(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
