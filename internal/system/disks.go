package system

import (
	"os"
)

// apaStatus reads the first sector of dev and reports whether it is a PS2 (APA) disk.
func apaStatus(dev string) string {
	f, err := os.Open(dev)
	if err != nil {
		if os.IsPermission(err) {
			return "no access (run with sudo)"
		}
		return "not readable"
	}
	defer f.Close()
	buf := make([]byte, 512)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return "not readable"
	}
	if string(buf[4:7]) == "APA" {
		return "PlayStation 2 HDD (APA)"
	}
	return "not PS2 formatted"
}

// Disk is a disk found on the system (device path and a description).
type Disk struct {
	Device string
	Info   string
}
