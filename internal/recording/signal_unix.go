//go:build !windows

package recording

import (
	"os"
	"syscall"
)

func interrupt(p *os.Process) error { return p.Signal(syscall.SIGINT) }
