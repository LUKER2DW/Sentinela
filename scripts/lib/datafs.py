#!/usr/bin/env python3
"""Root's access to the data disk (SENTINELA_DATA, /data). Run with python3 -I.

The data disk belongs to uid 1000, the containers' user, so every entry below
the data root may be swapped at any moment for a symbolic link, a FIFO or
another file. Checking a name and then using the same name again (test -L,
then install/touch/rm/cat) leaves a window in which the entry changes. Here
nothing is used by name twice: each operation walks from the data root one
component at a time with O_NOFOLLOW and then works only through the
descriptors it holds (openat, mkdirat, fchown, fchmod, unlinkat, renameat).

  datafs.py read  ROOT PATH MAX      print PATH, a plain file of at most MAX bytes
  datafs.py take  ROOT PATH MAX      the same, then remove it (a request file);
                                     anything else at PATH (a link, FIFO,
                                     socket, directory or oversized file) is
                                     removed too and refused, so a path unit
                                     watching PATH stops firing
  datafs.py rm    ROOT PATH...       remove what is at each PATH (missing ones
                                     are fine)
  datafs.py mkdir ROOT MODE PATH...  make directories (and missing parents),
                                     owned 1000:1000 with MODE, like
                                     install -d -o 1000 -g 1000 -m MODE
  datafs.py touch ROOT PATH          create an empty root-owned file when
                                     missing ("created PATH"); refuse anything
                                     but a plain file
  datafs.py write ROOT PATH MODE     replace PATH with stdin, owned 1000:1000
                                     with MODE (a temporary file with a random
                                     name, then rename)

PATH is below ROOT, absolute or relative to it, without "." or ".." parts.
ROOT itself is the operator's choice (a mount point in a root-owned
directory) and is the only name resolved normally. A plain file is regular,
has one link and belongs to uid 1000 or root. Removing never follows what it
removes: a link goes, not its target; an empty directory is removed and one
with entries renamed aside (.NAME.<random>.refused), never walked.
Exit status: 0 done, 2 missing (read, take), 1 refused or failed.
"""

import errno
import os
import secrets
import stat
import sys

UID = GID = 1000  # the containers' user and group (deploy/docker-compose.yml)
DIR = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
MISSING = 2


class Refused(Exception):
    pass


def parts(root, path):
    root = root.rstrip("/") or "/"
    if path.startswith("/"):
        prefix = root if root == "/" else root + "/"
        if not path.startswith(prefix):
            raise Refused(f"{path} is not below {root}")
        path = path[len(prefix):]
    names = path.split("/")
    if any(n in ("", ".", "..") for n in names):
        raise Refused(f"bad path below {root}: {path!r}")
    return names


def opendir(parent, name):
    try:
        return os.open(name, DIR, dir_fd=parent)
    except OSError as e:
        if e.errno in (errno.ELOOP, errno.ENOTDIR):
            raise Refused(f"{name} is not a plain directory") from None
        raise


def walk(root, names, mode=None):
    """The directory below root named by names, as a descriptor. With mode,
    missing directories are made, owned 1000:1000 with that mode."""
    fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        for name in names:
            try:
                nfd = opendir(fd, name)
            except FileNotFoundError:
                if mode is None:
                    raise
                try:
                    os.mkdir(name, 0o700, dir_fd=fd)
                except FileExistsError:
                    pass
                nfd = opendir(fd, name)
                own(nfd, mode)
            os.close(fd)
            fd = nfd
        return fd
    except BaseException:
        os.close(fd)
        raise


def own(fd, mode):
    os.fchown(fd, UID, GID)
    os.fchmod(fd, mode)


def plain(fd, what, limit=None):
    st = os.fstat(fd)
    if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_uid not in (0, UID):
        raise Refused(f"{what} is not a plain file")
    if limit is not None and st.st_size > limit:
        raise Refused(f"{what} is larger than {limit} bytes")


def unlink(dfd, name):
    try:
        os.unlink(name, dir_fd=dfd)
    except FileNotFoundError:
        pass


def discard(dfd, name):
    """Remove whatever is at name in dfd, without following it: a file, link
    (not its target), FIFO or socket is unlinked, an empty directory removed,
    and a directory with entries renamed aside, so the name is free. The
    entry may change kind between tries; a few rounds settle it."""
    for _ in range(4):
        try:
            os.unlink(name, dir_fd=dfd)
            return
        except FileNotFoundError:
            return
        except OSError as e:
            if e.errno not in (errno.EISDIR, errno.EPERM):
                raise
        try:
            os.rmdir(name, dir_fd=dfd)
            return
        except FileNotFoundError:
            return
        except NotADirectoryError:
            continue  # a file again: unlink it
        except OSError as e:
            if e.errno not in (errno.ENOTEMPTY, errno.EEXIST):
                raise
        try:
            os.rename(name, f".{name}.{secrets.token_hex(8)}.refused", src_dir_fd=dfd, dst_dir_fd=dfd)
            return
        except FileNotFoundError:
            return
    raise Refused(f"could not remove {name}")


def read(root, path, limit, take=False):
    names = parts(root, path)
    try:
        dfd = walk(root, names[:-1])
    except FileNotFoundError:
        return MISSING
    name = names[-1]
    try:
        try:
            # O_NONBLOCK: a FIFO opens at once (and is refused below).
            fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_NOCTTY | os.O_CLOEXEC, dir_fd=dfd)
        except FileNotFoundError:
            return MISSING
        except OSError as e:
            if take:
                discard(dfd, name)
            if e.errno == errno.ELOOP:
                raise Refused(f"{path} is a symbolic link") from None
            raise
        try:
            plain(fd, path, limit)
            data = b""
            while len(data) <= limit:
                chunk = os.read(fd, limit + 1 - len(data))
                if not chunk:
                    break
                data += chunk
            if len(data) > limit:
                raise Refused(f"{path} is larger than {limit} bytes")
        finally:
            os.close(fd)
            if take:
                discard(dfd, name)
    finally:
        os.close(dfd)
    sys.stdout.buffer.write(data)
    return 0


def rm(root, paths):
    for path in paths:
        names = parts(root, path)
        try:
            dfd = walk(root, names[:-1])
        except FileNotFoundError:
            continue
        try:
            discard(dfd, names[-1])
        finally:
            os.close(dfd)
    return 0


def mkdir(root, mode, paths):
    for path in paths:
        names = parts(root, path)
        pfd = walk(root, names[:-1], mode)
        try:
            try:
                os.mkdir(names[-1], 0o700, dir_fd=pfd)
            except FileExistsError:
                pass
            fd = opendir(pfd, names[-1])
        finally:
            os.close(pfd)
        try:
            own(fd, mode)
        finally:
            os.close(fd)
    return 0


def touch(root, path):
    names = parts(root, path)
    dfd = walk(root, names[:-1])
    try:
        created = True
        try:
            fd = os.open(names[-1], os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o644, dir_fd=dfd)
        except FileExistsError:
            created = False
            try:
                fd = os.open(names[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_NOCTTY | os.O_CLOEXEC, dir_fd=dfd)
            except OSError as e:
                if e.errno == errno.ELOOP:
                    raise Refused(f"{path} is a symbolic link") from None
                raise
        try:
            plain(fd, path)
        finally:
            os.close(fd)
    finally:
        os.close(dfd)
    if created:
        print(f"created {path}")
    return 0


def write(root, path, mode):
    names = parts(root, path)
    dfd = walk(root, names[:-1])
    try:
        tmp, fd = tempfile(dfd, names[-1])
        try:
            while True:
                chunk = sys.stdin.buffer.read(1 << 20)
                if not chunk:
                    break
                writeall(fd, chunk)
            own(fd, mode)
            os.fsync(fd)
        except BaseException:
            os.close(fd)
            unlink(dfd, tmp)
            raise
        os.close(fd)
        # rename never follows the target: a link planted at the name is
        # replaced, not written through.
        os.rename(tmp, names[-1], src_dir_fd=dfd, dst_dir_fd=dfd)
    finally:
        os.close(dfd)
    return 0


def tempfile(dfd, name):
    """A new file next to name, with a name nobody can guess beforehand: the
    folder belongs to uid 1000, which could otherwise plant something at a
    predictable name and make every write fail."""
    for _ in range(8):
        tmp = f".{name}.{secrets.token_hex(8)}.tmp"
        try:
            return tmp, os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=dfd)
        except FileExistsError:
            continue
    raise Refused(f"no free temporary name next to {name}")


def writeall(fd, data):
    view = memoryview(data)
    while view:
        n = os.write(fd, view)
        view = view[n:]


def mode_arg(s):
    m = int(s, 8)
    if m & ~0o777:
        raise Refused(f"bad mode {s}")
    return m


def main(argv):
    if len(argv) < 3:
        print("usage: datafs.py read|take|rm|mkdir|touch|write ROOT ... (see the header)", file=sys.stderr)
        return 1
    cmd, root, rest = argv[1], argv[2], argv[3:]
    if cmd in ("read", "take") and len(rest) == 2:
        return read(root, rest[0], int(rest[1]), take=cmd == "take")
    if cmd == "rm" and rest:
        return rm(root, rest)
    if cmd == "mkdir" and len(rest) >= 2:
        return mkdir(root, mode_arg(rest[0]), rest[1:])
    if cmd == "touch" and len(rest) == 1:
        return touch(root, rest[0])
    if cmd == "write" and len(rest) == 2:
        return write(root, rest[0], mode_arg(rest[1]))
    print(f"datafs.py: bad arguments: {' '.join(argv[1:])}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv))
    except Refused as e:
        print(f"refusing: {e}", file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError) as e:
        print(f"datafs.py: {e}", file=sys.stderr)
        sys.exit(1)
