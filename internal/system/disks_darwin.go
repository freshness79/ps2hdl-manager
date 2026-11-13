//go:build darwin

package system

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
)

// systemDisks lists physical disks using diskutil.
func SystemDisks(ctx context.Context) ([]Disk, error) {
	out, err := exec.CommandContext(ctx, "diskutil", "list").Output()
	if err != nil {
		return nil, err
	}
	head := regexp.MustCompile(`^(/dev/disk\d+)\s+\(([^)]*)\):`)
	size := regexp.MustCompile(`\*([\d.,]+\s*[KMGT]B)`)
	var disks []Disk
	var cur *Disk
	for _, l := range strings.Split(string(out), "\n") {
		if m := head.FindStringSubmatch(l); m != nil {
			cur = nil
			if !strings.Contains(m[2], "physical") {
				continue // skip synthesized / disk images
			}
			disks = append(disks, Disk{Device: m[1], Info: m[2]})
			cur = &disks[len(disks)-1]
			continue
		}
		if cur != nil && !strings.Contains(cur.Info, "B ") {
			if m := size.FindStringSubmatch(l); m != nil {
				cur.Info = m[1] + " " + cur.Info
			}
		}
	}
	for i := range disks {
		disks[i].Info += " — " + apaStatus(disks[i].Device)
	}
	return disks, nil
}

// mountedVolumes returns the mount points of volumes on dev (/dev/diskN or /dev/rdiskN).
func MountedVolumes(dev string) []string {
	base := strings.Replace(dev, "/dev/rdisk", "/dev/disk", 1)
	out, err := exec.Command("mount").Output()
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `(?:s\d+)* on (.+?) \(`)
	var mounts []string
	for _, l := range strings.Split(string(out), "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			mounts = append(mounts, m[1])
		}
	}
	return mounts
}

func SystemDiskNote(string) string { return "" }
