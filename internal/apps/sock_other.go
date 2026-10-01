//go:build windows

package apps

func socketGroup(string) string { return "" }

func selfUser() string { return "1000:1000" }
