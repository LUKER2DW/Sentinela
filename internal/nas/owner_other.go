//go:build !linux

package nas

import "os"

// Extended attributes are only used on the Linux server.
func getOwner(*os.File) string { return "" }

func setOwner(*os.File, string) {}
