package nas

import (
	"os"

	"golang.org/x/sys/unix"
)

// ownerAttr names who put a file or folder in the Nuvem.
const ownerAttr = "user.sentinela.owner"

func getOwner(f *os.File) string {
	buf := make([]byte, 128)
	n, err := unix.Fgetxattr(int(f.Fd()), ownerAttr, buf)
	if err != nil || n <= 0 {
		return ""
	}
	return string(buf[:n])
}

func setOwner(f *os.File, user string) {
	_ = unix.Fsetxattr(int(f.Fd()), ownerAttr, []byte(user), 0)
}
