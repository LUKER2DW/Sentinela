#!/usr/bin/env bash
# Write SMART health of the disks behind the Sentinela mounts to a JSON file
# the API can read (read-only mount). Runs as root from a systemd timer.
#   scripts/disk-health.sh [/var/lib/sentinela/disks.json]
set -euo pipefail
OUT="${1:-/var/lib/sentinela/disks.json}"
MOUNTS=(/data /srv/nas /srv/backup /)
python3 - "$OUT" "${MOUNTS[@]}" <<'PY'
import json, os, subprocess, sys, tempfile, time
out, mounts = sys.argv[1], sys.argv[2:]
def disk_of(mount):
    src = subprocess.run(["findmnt", "-no", "SOURCE", mount], capture_output=True, text=True).stdout.strip()
    if not src:
        return None
    pk = subprocess.run(["lsblk", "-no", "PKNAME", src], capture_output=True, text=True).stdout.strip().splitlines()
    return "/dev/" + pk[0] if pk and pk[0] else src
def attr(table, ident):
    for a in table:
        if a.get("id") == ident:
            return a.get("raw", {}).get("value", 0)
    return 0
res = {"updated": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "mounts": {}}
cache = {}
for m in mounts:
    dev = disk_of(m)
    if not dev:
        continue
    if dev not in cache:
        p = subprocess.run(["smartctl", "-j", "-H", "-A", "-i", "-l", "selftest", dev], capture_output=True, text=True)
        try:
            j = json.loads(p.stdout)
        except ValueError:
            j = {}
        t = j.get("ata_smart_attributes", {}).get("table", [])
        tests = j.get("ata_smart_self_test_log", {}).get("standard", {}).get("table", [])
        last = tests[0]["status"]["string"] if tests else ""
        realloc, pending, uncorr = attr(t, 5), attr(t, 197), attr(t, 187)
        passed = j.get("smart_status", {}).get("passed", True)
        state = "ok"
        if pending or uncorr > 0 or "failure" in last.lower():
            state = "warn"
        if not passed:
            state = "fail"
        cache[dev] = {
            "device": dev, "model": j.get("model_name", ""), "state": state,
            "passed": passed, "reallocated": realloc, "pending": pending, "uncorrectable": uncorr,
            "temperature": j.get("temperature", {}).get("current"),
            "power_on_hours": j.get("power_on_time", {}).get("hours"),
            "last_selftest": last,
        }
    res["mounts"][m] = cache[dev]
os.makedirs(os.path.dirname(out), exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(out))
with os.fdopen(fd, "w") as f:
    json.dump(res, f, indent=1)
os.chmod(tmp, 0o644)
os.replace(tmp, out)
PY
