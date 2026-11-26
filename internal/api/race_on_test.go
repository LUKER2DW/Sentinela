//go:build race

package api

// raceEnabled: the race detector changes allocation and stack sizes, so
// memory measurements are skipped under it.
const raceEnabled = true
