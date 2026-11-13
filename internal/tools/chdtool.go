package tools

import (
	"context"
	"fmt"
	chdlib "github.com/freshness79/ps2hdl-manager/internal/chd"
	"os"
	"path/filepath"
	"strings"
)

// CHDTool handles CHD images with the built-in pure-Go reader/writer
// (internal/chd): CHD v3, v4 and v5.
type CHDTool struct{}

func pctLine(cb LineFunc, what string) func(float64) {
	last := -1
	return func(f float64) {
		p := int(f * 100)
		if cb != nil && p != last {
			last = p
			cb(fmt.Sprintf("%s %d%%", what, p))
		}
	}
}

// Kind returns "DVD" or "CD" (from the CHD metadata).
func (c *CHDTool) Kind(ctx context.Context, path string) (string, error) {
	f, err := chdlib.Open(path)
	if err == nil {
		defer f.Close()
		if k := f.Kind(); k != "" {
			return k, nil
		}
		return "", fmt.Errorf("%s: not a CD/DVD CHD (hard disk or unknown CHD)", filepath.Base(path))
	}
	return "", err
}

// ToISO decompresses a CHD into a 2048-byte-sector ISO in workDir.
// Raw CD sectors (2352/2336) are converted; only the first data track is kept.
func (c *CHDTool) ToISO(ctx context.Context, chd, workDir string, cb LineFunc) (string, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", err
	}
	iso := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(chd), filepath.Ext(chd))+".iso")
	err := chdlib.ExtractISO(chd, iso, pctLine(cb, "Decompressing CHD…"))
	if err == nil {
		return iso, nil
	}
	return "", err
}

// CreateDVD compresses a 2048-byte ISO into a DVD CHD (built-in writer: zlib, 16 KB hunks).
func (c *CHDTool) CreateDVD(ctx context.Context, iso, out string, cb LineFunc) error {
	return chdlib.CreateDVD(iso, out, chdlib.WriteOptions{}, pctLine(cb, "Compressing CHD…"))
}

// Version returns the CHD format version of a file (3, 4 or 5).
func (c *CHDTool) Version(path string) (int, error) {
	f, err := chdlib.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Version, nil
}
