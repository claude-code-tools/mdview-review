//go:build windows

package rendezvous

import "os"

// Alive is best-effort on Windows. os.FindProcess always succeeds — it allocates a
// handle without probing liveness — so this may return true for a dead pid. Owner-watch
// and replace-on-reuse are POSIX-first; Windows relies on no-client / max-lifetime instead.
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
