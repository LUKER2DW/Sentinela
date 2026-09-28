//go:build !linux

package sysinfo

import "runtime"

// DiskUsage is unsupported outside Linux; it reports an empty disk so
// retention never deletes on development machines.
func DiskUsage(string) (Disk, error) { return newDisk(0, 0), nil }

// Snapshot returns what the Go runtime knows.
func Snapshot() Host {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return Host{NumCPU: runtime.NumCPU(), ProcRSS: ms.Sys}
}
