//go:build windows

package system

import (
	"context"
	"fmt"
	"strings"
)

func SystemDisks(ctx context.Context) ([]Disk, error) {
	return nil, fmt.Errorf("this hdl_dump build has no 'query' command: enter the device manually (e.g. hdd1:)")
}

// mountedVolumes is not available without extra APIs on Windows (see systemDiskNote).
func MountedVolumes(string) []string { return nil }

// systemDiskNote warns about disk 0, normally the Windows system disk.
func SystemDiskNote(dev string) string {
	if strings.EqualFold(strings.TrimSpace(dev), "hdd0:") {
		return "Disk 0 is normally the Windows system disk! Check the number in Disk Management first."
	}
	return ""
}
