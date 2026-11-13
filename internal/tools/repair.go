package tools

import (
	"github.com/freshness79/ps2hdl-manager/internal/apa"
)

// dirtyPartitions lists the main partitions whose 32-byte id field holds stray
// bytes after the name. The PS2 APA driver (OPL, HDD browser, pfsshell) compares
// the whole field, so it may not find such a partition by name.
func DirtyPartitions(dev string) ([]apa.Partition, error) {
	d, err := apa.Scan(dev)
	if err != nil {
		return nil, err
	}
	var out []apa.Partition
	for _, p := range d.Partitions {
		if p.DirtyID && p.IsMain() {
			out = append(out, p)
		}
	}
	return out, nil
}
