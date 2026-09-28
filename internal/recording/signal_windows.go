//go:build windows

package recording

import "os"

// Windows cannot deliver SIGINT to a child; fragmented MP4 survives a kill.
func interrupt(p *os.Process) error { return p.Kill() }
