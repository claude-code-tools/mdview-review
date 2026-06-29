//go:build !windows

package rendezvous

import "syscall"

// Alive reports whether pid is a live process via a signal-0 probe.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func terminate(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }
