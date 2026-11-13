package system

import (
	"errors"
	"github.com/freshness79/ps2hdl-manager/internal/apa"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

// Access is the result of a disk permission check (nothing is written).
type Access struct {
	Device  string
	Path    string // what is actually opened (\\.\PhysicalDriveN on Windows)
	Read    bool
	Write   bool
	Busy    bool // write refused because the disk is in use (mounted volumes)
	Missing bool
	ReadErr error
	WErr    error
	Admin   bool   // running with administrator/root rights
	Hint    string // what to do, empty when everything is fine
}

// checkAccess opens the device for reading and for writing (then closes it at
// once, without writing) to find out what the app will be allowed to do.
func CheckAccess(dev string) Access {
	a := Access{Device: dev, Path: apa.OpenPath(dev), Admin: isAdmin()}
	if f, err := os.Open(a.Path); err == nil {
		a.Read = true
		f.Close()
	} else {
		a.ReadErr = err
		a.Missing = errors.Is(err, fs.ErrNotExist)
	}
	if !a.Missing {
		if f, err := os.OpenFile(a.Path, os.O_RDWR, 0); err == nil {
			a.Write = true
			f.Close()
		} else {
			a.WErr = err
			a.Busy = isBusy(err)
		}
	}
	a.Hint = accessHint(a)
	return a
}

// Summary is a one-line description for the status chip.
func (a Access) Summary() string {
	switch {
	case a.Missing:
		return "Disk not found"
	case a.Read && a.Write:
		return "Read & write"
	case a.Read && a.Busy:
		return "Read only (disk in use)"
	case a.Read:
		return "Read only"
	}
	return "No access"
}

func accessHint(a Access) string {
	self, _ := os.Executable()
	switch {
	case a.Missing:
		return "The disk is not connected (or its name changed): use “Detect disks”."
	case a.Read && a.Write:
		return ""
	case a.Busy && runtime.GOOS == "darwin":
		return "A volume on this disk is mounted, so macOS does not allow writing to it.\n" +
			"Unmount it while keeping the disk connected, e.g. in Terminal:\n\n    diskutil unmountDisk " + strings.Replace(a.Device, "/dev/rdisk", "/dev/disk", 1)
	case a.Busy:
		return "The disk is in use (mounted volumes or another program): unmount/close them and check again."
	}
	switch runtime.GOOS {
	case "darwin":
		return "macOS lets only administrators access disks directly.\n" +
			"Quit the app and start it from Terminal with:\n\n    sudo \"" + self + "\""
	case "windows":
		return "Writing to disks needs administrator rights:\n" +
			"close the app, right-click it and choose “Run as administrator”."
	default:
		return "Your user may not access this disk. Either start the app with:\n\n    sudo \"" + self + "\"\n\n" +
			"or add your user to the group that owns the disk (usually 'disk'), then log out and in:\n\n" +
			"    sudo usermod -aG disk $USER"
	}
}
