//go:build !windows

package apps

import "syscall"

// openNonblock makes opening a FIFO a workload planted return at once
// instead of waiting for a writer; the file type is checked after opening.
const openNonblock = syscall.O_NONBLOCK

// openNoFollow refuses a symbolic link as the last path element.
const openNoFollow = syscall.O_NOFOLLOW
