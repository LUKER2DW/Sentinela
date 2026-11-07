// Package sysinfo reads disk, memory and CPU figures for the System page and
// for retention decisions. Linux is the target; other OSes get best effort.
package sysinfo

import "time"

// Disk is filesystem usage for a path.
type Disk struct {
	Total uint64  `json:"total"`
	Free  uint64  `json:"free"`
	Used  uint64  `json:"used"`
	Pct   float64 `json:"pct"`
}

// Host is a host-level snapshot.
type Host struct {
	CPUPct    float64       `json:"cpu_pct"`
	MemTotal  uint64        `json:"mem_total"`
	MemAvail  uint64        `json:"mem_available"`
	Uptime    time.Duration `json:"uptime_ns"`
	Load1     float64       `json:"load1"`
	ProcRSS   uint64        `json:"proc_rss"`
	NumCPU    int           `json:"num_cpu"`
	Supported bool          `json:"supported"`
}

func newDisk(total, free uint64) Disk {
	d := Disk{Total: total, Free: free}
	if total > 0 {
		d.Used = total - free
		d.Pct = float64(d.Used) * 100 / float64(total)
	}
	return d
}
