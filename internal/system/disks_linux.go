//go:build linux

package system

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// systemDisks lists whole disks using lsblk.
func SystemDisks(ctx context.Context) ([]Disk, error) {
	out, err := exec.CommandContext(ctx, "lsblk", "-dnpo", "NAME,SIZE,TYPE,MODEL").Output()
	if err != nil {
		return nil, err
	}
	var disks []Disk
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[2] != "disk" {
			continue
		}
		info := f[1] + " " + strings.Join(f[3:], " ")
		disks = append(disks, Disk{Device: f[0], Info: strings.TrimSpace(info) + " — " + apaStatus(f[0])})
	}
	return disks, nil
}

// mountedVolumes returns the mount points of filesystems on dev or its partitions.
func MountedVolumes(dev string) []string {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && (f[0] == dev || strings.HasPrefix(f[0], dev) && isPartSuffix(f[0][len(dev):])) {
			out = append(out, f[1])
		}
	}
	return out
}

// isPartSuffix: "1", "p1" (nvme/mmc/loop partitions).
func isPartSuffix(s string) bool {
	s = strings.TrimPrefix(s, "p")
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func SystemDiskNote(string) string { return "" }
