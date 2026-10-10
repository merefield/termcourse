package ui

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func appleTerminalSupportsTrueColor() bool {
	release, err := unix.Sysctl("kern.osrelease")
	if err != nil {
		return false
	}
	major, _, _ := strings.Cut(release, ".")
	version, err := strconv.Atoi(major)
	// Darwin 25 is macOS Tahoe (26).
	return err == nil && version >= 25
}
