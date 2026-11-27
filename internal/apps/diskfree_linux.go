//go:build linux

package apps

import "golang.org/x/sys/unix"

// fsSpace is the free and total bytes of the filesystem holding dir (free
// as an unprivileged process sees it).
func fsSpace(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}
