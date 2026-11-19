# shellcheck shell=bash
# Helpers for the scripts that run as root and touch the data disk
# (SENTINELA_DATA, /data). Sourced, never run.
#
# The data disk is owned by uid 1000, the user of the containers, so what
# lives there is untrusted from root's point of view: a compromised container
# could replace a directory or a file by a symbolic link, and a root script
# that chown/chmod/writes through it would act on the link's target. These
# helpers refuse a symbolic link instead of following it. They narrow the
# window to the instant between the check and the use; root scripts should
# still read nothing from the data disk that they do not validate.

# refuse_symlink PATH...: fail when any PATH itself is a symbolic link.
refuse_symlink() {
  local p
  for p in "$@"; do
    if [[ -L "$p" ]]; then
      echo "refusing: $p is a symbolic link" >&2
      return 1
    fi
  done
}

# refuse_symlink_below ROOT PATH: fail when PATH, or any directory between
# ROOT (exclusive: the mount point is the operator's choice) and PATH, is a
# symbolic link, or when PATH is not below ROOT or has a ".." part.
refuse_symlink_below() {
  local root="${1%/}" p="$2" cur part
  local -a parts
  if [[ "$p" != "$root"/* ]]; then
    echo "refusing: $p is not below $root" >&2
    return 1
  fi
  IFS=/ read -ra parts <<<"${p#"$root"/}"
  cur="$root"
  for part in "${parts[@]}"; do
    [[ -n "$part" ]] || continue
    if [[ "$part" == .. ]]; then
      echo "refusing: $p has a '..' part" >&2
      return 1
    fi
    cur="$cur/$part"
    refuse_symlink "$cur" || return 1
  done
}

# safe_install_d ROOT MODE DIR...: install -d -o 1000 -g 1000 -m MODE for
# directories below ROOT, after refusing symbolic links on the way, and
# once more afterwards (the directory must still be a plain directory).
safe_install_d() {
  local root="$1" mode="$2" d
  shift 2
  for d in "$@"; do
    refuse_symlink_below "$root" "$d" || return 1
  done
  install -d -o 1000 -g 1000 -m "$mode" "$@"
  for d in "$@"; do
    refuse_symlink_below "$root" "$d" || return 1
    [[ -d "$d" ]] || { echo "refusing: $d is not a directory" >&2; return 1; }
  done
}
