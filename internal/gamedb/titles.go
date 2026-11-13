package gamedb

import (
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"path/filepath"
	"regexp"
	"strings"
)

// cleanFileTitle turns "Sample Game Delta (Europe) (En,Fr).chd" into "Sample Game Delta".
func CleanFileTitle(p string) string {
	base := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	base = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]`).ReplaceAllString(base, "")
	base = disc.FileIDRe.ReplaceAllString(base, "")
	return CleanTitle(strings.Trim(base, " ._-"))
}
