//go:build !linux

package apps

// fsSpace is unknown outside Linux (development builds): no reserve applies.
func fsSpace(string) (free, total uint64, err error) { return 0, 0, nil }
