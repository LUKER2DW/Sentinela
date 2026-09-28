//go:build linux

package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DiskUsage returns usage of the filesystem holding path.
func DiskUsage(path string) (Disk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{}, err
	}
	bs := uint64(st.Bsize)
	// Bavail: space available to unprivileged users, what ffmpeg can use.
	return newDisk(st.Blocks*bs, st.Bavail*bs), nil
}

var (
	cpuMu               sync.Mutex
	lastIdle, lastTotal uint64
)

func readCPU() (idle, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0
	}
	fields := strings.Fields(sc.Text())
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return idle, total
}

func meminfo() (total, avail uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(fs[1], 10, 64)
		switch fs[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	return
}

func selfRSS() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fs := strings.Fields(string(b))
	if len(fs) < 2 {
		return 0
	}
	n, _ := strconv.ParseUint(fs[1], 10, 64)
	return n * uint64(os.Getpagesize())
}

// Snapshot returns host figures. CPU% is measured since the previous call.
func Snapshot() Host {
	h := Host{Supported: true, NumCPU: runtime.NumCPU()}
	idle, total := readCPU()
	cpuMu.Lock()
	if lastTotal > 0 && total > lastTotal {
		dt := float64(total - lastTotal)
		h.CPUPct = (dt - float64(idle-lastIdle)) * 100 / dt
	}
	lastIdle, lastTotal = idle, total
	cpuMu.Unlock()
	h.MemTotal, h.MemAvail = meminfo()
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64); err == nil {
			h.Uptime = time.Duration(f * float64(time.Second))
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		h.Load1, _ = strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	}
	h.ProcRSS = selfRSS()
	return h
}
