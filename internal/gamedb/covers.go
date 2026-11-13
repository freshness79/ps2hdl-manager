package gamedb

import (
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OPL cover size.
const CoverW, CoverH = 140, 200

func ArtCacheDir() string {
	d := filepath.Join(config.Dir(), "art")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// CoverFileName returns the OPL file name, e.g. SLUS_900.02_COV.png.
func CoverFileName(startup string) string { return ArtFileName(startup, "COV") }

// CachedCover returns the path of the cached (already resized) cover, if any.
func CachedCover(startup string) string { return CachedArt(startup, "COV") }

// DownloadCover fetches the cover for startup from urlTemplate ({ID} = SLUS-90002,
// {STARTUP} = SLUS_900.02), resizes it to 140x200 and stores it in the cache.
func DownloadCover(urlTemplate, startup string) (string, error) {
	n := disc.NormalizeSerial(startup)
	if n == "" {
		return "", fmt.Errorf("invalid startup ID %q", startup)
	}
	id := n[:4] + "-" + n[4:]
	u := strings.NewReplacer("{ID}", id, "{STARTUP}", disc.StartupFromSerial(n)).Replace(urlTemplate)
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "ps2hdl-manager/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return "", fmt.Errorf("no cover online for %s", id)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cover download: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return "", err
	}
	return savePicture(b, ArtTypes[0], startup)
}

// ImportCover converts a local image (jpg/png/gif) into the cached OPL cover.
func ImportCover(path, startup string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return savePicture(b, ArtTypes[0], startup)
}

// resizeFill scales img to exactly w×h (box filter when shrinking, bilinear otherwise).
func resizeFill(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sx := float64(sw) / float64(w)
	sy := float64(sh) / float64(h)
	for y := 0; y < h; y++ {
		y0 := float64(y) * sy
		y1 := y0 + sy
		for x := 0; x < w; x++ {
			x0 := float64(x) * sx
			x1 := x0 + sx
			var r, g, bl, a, n float64
			for yy := int(y0); yy < int(y1+0.999) && yy < sh; yy++ {
				for xx := int(x0); xx < int(x1+0.999) && xx < sw; xx++ {
					cr, cg, cb, ca := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					r += float64(cr)
					g += float64(cg)
					bl += float64(cb)
					a += float64(ca)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}
