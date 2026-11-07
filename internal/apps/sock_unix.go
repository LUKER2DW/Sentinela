//go:build !windows

package apps

import (
	"os"
	"strconv"
	"syscall"
)

// socketGroup is the group owning the Docker socket (helpers get it as an
// extra group instead of running as root).
func socketGroup(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(s.Gid), 10)
	}
	return ""
}

// selfUser is the agent's uid:gid, which owns every file it writes.
func selfUser() string { return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()) }
