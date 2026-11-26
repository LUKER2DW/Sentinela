# shellcheck shell=bash
# Helpers for the scripts that run as root and touch the data disk
# (SENTINELA_DATA, /data). Sourced, never run.
#
# The data disk is owned by uid 1000, the user of the containers, so what
# lives there is untrusted from root's point of view: a compromised container
# can swap any directory or file below the data root for a symbolic link, a
# FIFO or another file at any moment, also between a check and the next
# command. So root never checks a name and then uses it again: every read,
# write, removal or directory below the data root goes through datafs.py,
# which walks from the root without following links and works through
# descriptors (see its header). Root scripts still validate what they read.

SAFE_LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

_datafs() { python3 -I "$SAFE_LIB/datafs.py" "$@"; }

# safe_install_d ROOT MODE DIR...: install -d -o 1000 -g 1000 -m MODE for
# directories below ROOT (missing parents too), without following links.
safe_install_d() { local root="$1" mode="$2"; shift 2; _datafs mkdir "$root" "$mode" "$@"; }

# safe_read ROOT FILE MAX: print FILE (below ROOT), a plain file of at most
# MAX bytes. Status 2 when it is missing, 1 when refused.
safe_read() { _datafs read "$1" "$2" "$3"; }

# safe_take ROOT FILE MAX: safe_read, then remove FILE (a request file). A
# link, FIFO or oversized file in its place is removed and refused.
safe_take() { _datafs take "$1" "$2" "$3"; }

# safe_rm ROOT FILE...: remove files below ROOT (missing ones are fine).
safe_rm() { local root="$1"; shift; _datafs rm "$root" "$@"; }

# safe_touch ROOT FILE: create an empty root-owned FILE when missing (prints
# "created FILE"); fail when something other than a plain file is there.
safe_touch() { _datafs touch "$1" "$2"; }

# safe_write ROOT FILE MODE: replace FILE with stdin, owned 1000:1000 with MODE.
safe_write() { _datafs write "$1" "$2" "$3"; }
