package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessCheck(t *testing.T) {
	f := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(f, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := CheckAccess(f); !a.Read || !a.Write || a.Hint != "" || a.Summary() != "Read & write" {
		t.Errorf("writable file: %+v", a)
	}
	if a := CheckAccess("/dev/does-not-exist"); !a.Missing || a.Summary() != "Disk not found" || a.Hint == "" {
		t.Errorf("missing: %+v", a)
	}
}

// Run as an unprivileged user (see `make test-access`): ACCESS_RO = a root-owned
// 0644 file, ACCESS_NONE = a root-owned 0600 file or a disk device.
func TestAccessNonRoot(t *testing.T) {
	ro, none := os.Getenv("ACCESS_RO"), os.Getenv("ACCESS_NONE")
	if ro == "" || none == "" || os.Geteuid() == 0 {
		t.Skip("needs ACCESS_RO / ACCESS_NONE and a non-root user")
	}
	a := CheckAccess(ro)
	if !a.Read || a.Write || a.Summary() != "Read only" || !strings.Contains(a.Hint, "sudo") {
		t.Errorf("read-only: %+v", a)
	}
	b := CheckAccess(none)
	if b.Read || b.Write || b.Summary() != "No access" || b.Hint == "" {
		t.Errorf("no access: %+v", b)
	}
	t.Logf("hint shown to the user:\n%s", b.Hint)
}

func TestMountedVolumes(t *testing.T) {
	if _, err := os.Stat("/proc/mounts"); err != nil {
		t.Skip()
	}
	b, _ := os.ReadFile("/proc/mounts")
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.HasPrefix(f[0], "/dev/") {
			if m := MountedVolumes(f[0]); len(m) == 0 {
				t.Errorf("%s mounted on %s not detected", f[0], f[1])
			}
			return
		}
	}
	t.Skip("no /dev mounts here")
}
