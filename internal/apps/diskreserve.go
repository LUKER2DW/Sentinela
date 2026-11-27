package apps

import (
	"context"
	"errors"
	"fmt"
)

// The system disk is shared with the NVR (its images, logs and the OS), and
// what services write to it has no per-service quota: their image layers,
// named volumes and the folders they bind each grow on their own, and the
// default ext4 has no project quotas to cap them with. So the agent keeps a
// reserve of the filesystem holding apps.state_dir (Docker's data root is on
// it in a default install): below the reserve no service starts, deploys,
// builds or pulls, and every running one is stopped. They start again once
// the free space is back above the reserve and a margin (ADR-0021).

// errDiskReserve marks a refusal because of the reserve.
var errDiskReserve = errors.New("the system disk is below its reserve")

// diskReserveFloor is the smallest reserve, whatever the percentage.
const diskReserveFloor = 1 << 30

// diskState reports the free bytes and the reserve; known is false when
// the filesystem cannot be read (no reserve applies then).
func (a *Agent) diskState() (free, reserve, total uint64, known bool) {
	free, total, err := a.diskSpace(a.Cfg.StateDir)
	if err != nil || total == 0 {
		return 0, 0, 0, false
	}
	pct := a.Cfg.DiskReservePercent
	if pct <= 0 {
		pct = 10
	}
	reserve = max(total/100*uint64(pct), diskReserveFloor)
	return free, reserve, total, true
}

// diskMargin is how far above the reserve the free space must be before
// services start again, so a disk hovering at the reserve does not make
// them stop and start in turn.
func diskMargin(total uint64) uint64 { return max(total/50, 512<<20) }

// diskGate refuses a start, deploy, build or pull below the reserve (or,
// with resume, below the reserve plus the margin).
func (a *Agent) diskGate(resume bool) error {
	free, reserve, total, known := a.diskState()
	if !known {
		return nil
	}
	need := reserve
	if resume {
		need += diskMargin(total)
	}
	if free >= need {
		return nil
	}
	return fmt.Errorf("%w: %s free, %s are kept for the NVR and the system (apps.disk_reserve_percent); free space on it (a service's files, unused images) and the services start again", errDiskReserve, mib(free), mib(need))
}

func mib(n uint64) string { return fmt.Sprintf("%d MB", n>>20) }

// holdingForDisk reports whether services are held for the disk now (a
// start then needs the margin as well).
func (a *Agent) holdingForDisk() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.diskLow
}

// enforceDiskReserve stops every running service while the disk is below
// the reserve. It reports whether the reserve holds services back.
func (a *Agent) enforceDiskReserve(ctx context.Context) bool {
	err := a.diskGate(false)
	a.mu.Lock()
	was := a.diskLow
	a.mu.Unlock()
	if err == nil {
		if was && a.diskGate(true) == nil {
			a.mu.Lock()
			a.diskLow = false
			a.mu.Unlock()
			a.Log.Info("apps: system disk above its reserve again; services start")
			return false
		}
		return was
	}
	a.mu.Lock()
	a.diskLow = true
	ids := make([]string, 0, len(a.apps))
	for _, app := range a.apps {
		ids = append(ids, app.ID)
	}
	a.mu.Unlock()
	if !was {
		a.Log.Warn("apps: system disk below its reserve, stopping the services", "err", err)
	}
	for _, id := range ids {
		app := a.snapshot(id)
		if app == nil || app.Deployed == nil {
			continue
		}
		stopped := false
		if app.Mode == ModeCompose {
			members, lerr := a.projectContainers(ctx, id)
			if lerr != nil {
				continue
			}
			for _, m := range members {
				if m.Running {
					if serr := a.Eng.Stop(ctx, m.Name, stopGrace); serr == nil {
						stopped = true
					}
				}
			}
		} else if st, ierr := a.Eng.Inspect(ctx, containerName(id)); ierr == nil && st != nil && st.Running {
			stopped = a.Eng.Stop(ctx, containerName(id), stopGrace) == nil
		}
		a.mu.Lock()
		r := a.rt(id)
		if app.Mode == ModeCompose {
			r.composeUp = false // the next start runs `up` again, through the gate
		}
		msg := "Stopped: " + err.Error()
		if stopped {
			a.eventLocked(id, "disk", msg)
		}
		r.err = msg
		a.mu.Unlock()
	}
	return true
}
