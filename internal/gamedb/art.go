package gamedb

// OPL art: the pictures OPL shows for a game, stored in +OPL/ART as
// <ID>_<TYPE>.png. OPL ≥ 1.2 (Oct 2024) only loads PNG; older builds also read
// JPG, so PNG works everywhere.

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/freshness79/ps2hdl-manager/internal/buildinfo"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
)

// ArtType is one kind of OPL picture.
type ArtType struct {
	Key     string // file suffix: COV, COV2, ICO…
	W, H    int    // size OPL draws it at
	Label   string
	Desc    string
	Default bool   // enabled by default (used by OPL's default theme)
	oplm    string // member suffix in OPL Manager's backup
	contain bool   // fit inside the box (logos) instead of stretching
}

// ArtTypes lists the OPL art types (sizes as used by OPL Manager and themes).
var ArtTypes = []ArtType{
	{"COV", 140, 200, "Front cover", "game list (default theme)", true, "COV", false},
	{"ICO", 64, 64, "Disc icon", "game list item icon (default theme)", true, "ICO", false},
	{"BG", 640, 480, "Background", "info page background (default theme)", true, "BG_00", false},
	{"SCR", 250, 188, "Screenshot 1", "info page (default theme)", true, "SCR_00", false},
	{"SCR2", 250, 188, "Screenshot 2", "info page (default theme)", true, "SCR_01", false},
	{"COV2", 242, 344, "Back cover", "custom themes only", false, "COV2", false},
	{"LAB", 18, 240, "Spine", "custom themes only", false, "LAB", false},
	{"LGO", 300, 125, "Logo", "custom themes only", false, "LGO", true},
}

// ArtTypeByKey returns the type with key k.
func ArtTypeByKey(k string) (ArtType, bool) {
	for _, t := range ArtTypes {
		if t.Key == strings.ToUpper(k) {
			return t, true
		}
	}
	return ArtType{}, false
}

// ArtFileName is the OPL file name: SLUS_900.02_COV.png.
func ArtFileName(startup, key string) string {
	n := disc.NormalizeSerial(startup)
	if n == "" {
		return ""
	}
	return disc.StartupFromSerial(n) + "_" + key + ".png"
}

// LegacyArtNames are names older versions of this app (or other tools) wrote,
// which OPL ≥ 1.2 ignores: removed when the PNG is written.
func LegacyArtNames(startup, key string) []string {
	n := disc.NormalizeSerial(startup)
	if n == "" {
		return nil
	}
	st := disc.StartupFromSerial(n)
	return []string{st + "_" + key + ".jpg", st + "_" + key + ".JPG", st + "_" + key + ".jpeg"}
}

// AllArtNames lists every ART file name a game can have (all types, PNG and
// legacy JPG), for deleting a game's pictures.
func AllArtNames(startup string) []string {
	var out []string
	for _, t := range ArtTypes {
		if f := ArtFileName(startup, t.Key); f != "" {
			out = append(out, f)
			out = append(out, LegacyArtNames(startup, t.Key)...)
		}
	}
	return out
}

// DefaultArtKeys are the types OPL's default theme shows.
func DefaultArtKeys() []string {
	var out []string
	for _, t := range ArtTypes {
		if t.Default {
			out = append(out, t.Key)
		}
	}
	return out
}

// CachedArt returns the cached picture of a type, if any.
func CachedArt(startup, key string) string {
	f := ArtFileName(startup, key)
	if f == "" {
		return ""
	}
	p := filepath.Join(ArtCacheDir(), f)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if key == "COV" { // cover cached by an older version as JPG: convert once
		old := strings.TrimSuffix(p, ".png") + ".jpg"
		if b, err := os.ReadFile(old); err == nil {
			if out, err := savePicture(b, ArtTypes[0], startup); err == nil {
				os.Remove(old)
				return out
			}
		}
	}
	return ""
}

// savePicture normalises a picture to the type's size and stores it as PNG in
// the cache. A PNG already at the right size is kept byte for byte (OPL
// Manager's backup ships 8-bit palette PNGs, which OPL loads as small textures).
func savePicture(data []byte, t ArtType, startup string) (string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("unsupported image: %w", err)
	}
	out := data
	if format != "png" || cfg.Width != t.W || cfg.Height != t.H {
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("unsupported image: %w", err)
		}
		var dst image.Image
		if t.contain {
			dst = resizeContain(img, t.W, t.H)
		} else {
			dst = resizeFill(img, t.W, t.H)
		}
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst); err != nil {
			return "", err
		}
		out = buf.Bytes()
	}
	p := filepath.Join(ArtCacheDir(), ArtFileName(startup, t.Key))
	if err := os.WriteFile(p, out, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// resizeContain fits img inside w×h keeping its proportions, on a transparent background.
func resizeContain(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return image.NewNRGBA(image.Rect(0, 0, w, h))
	}
	tw, th := w, h
	if sw*h > sh*w {
		th = max(1, sh*w/sw)
	} else {
		tw = max(1, sw*h/sh)
	}
	scaled := resizeFill(img, tw, th)
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, image.Rect((w-tw)/2, (h-th)/2, (w-tw)/2+tw, (h-th)/2+th), scaled, image.Point{}, draw.Over)
	return dst
}

// ---------------------------------------------------------------- OPL Manager backup

// OPLMItem is OPL Manager's last complete art backup on archive.org.
const OPLMItem = "OPLM_ART_2024_09"

// OPLMArchiveURL returns the download URL of a backup item.
func OPLMArchiveURL(item string) string {
	return "https://archive.org/download/" + item + "/" + item + ".zip"
}

// OPLMArt reads pictures from OPL Manager's art backup (PS2/<ID>/<ID>_<TYPE>.png).
type OPLMArt struct {
	URL string

	mu      sync.Mutex
	failed  time.Time // last failed open: not retried for a while
	openErr error
	zip     *RemoteZip
	byKey   map[string]*zip.File // "SLUS_900.02_COV" → member
	limiter chan struct{}        // archive.org is slow: few requests at a time
}

// NewOPLMArt prepares a source; nothing is downloaded until a picture is asked for.
func NewOPLMArt(url string) *OPLMArt {
	return &OPLMArt{URL: url, limiter: make(chan struct{}, 2)}
}

func (o *OPLMArt) open() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.zip != nil {
		return nil
	}
	if time.Since(o.failed) < 5*time.Minute {
		return o.openErr
	}
	o.openErr = nil
	func() {
		cache := filepath.Join(config.Dir(), "art-index", filepath.Base(o.URL)+".idx")
		z, err := OpenRemoteZip(o.URL, cache, "ps2hdl-manager/"+buildinfo.Version)
		if err != nil {
			o.failed, o.openErr = time.Now(), fmt.Errorf("OPL Manager art archive: %w", err)
			return
		}
		o.byKey = map[string]*zip.File{}
		for _, f := range z.Members() {
			parts := strings.Split(f.Name, "/")
			if len(parts) != 3 || !strings.EqualFold(parts[0], "PS2") {
				continue // PS1 pictures could take a PS2 game's slot
			}
			base := strings.ToUpper(strings.TrimSuffix(parts[2], filepath.Ext(parts[2])))
			key := base
			if prev, ok := o.byKey[key]; ok && strings.EqualFold(filepath.Ext(prev.Name), ".png") {
				continue // prefer PNG when both exist
			}
			o.byKey[key] = f
		}
		o.zip = z
	}()
	return o.openErr
}

// Available lists the art types the backup has for a game (opens the index).
func (o *OPLMArt) Available(startup string) ([]string, error) {
	if err := o.open(); err != nil {
		return nil, err
	}
	n := disc.NormalizeSerial(startup)
	if n == "" {
		return nil, nil
	}
	st := disc.StartupFromSerial(n)
	var out []string
	for _, t := range ArtTypes {
		if _, ok := o.byKey[st+"_"+t.oplm]; ok {
			out = append(out, t.Key)
		}
	}
	return out, nil
}

// Fetch downloads one picture of a game into the cache (errNoArt if the backup
// has none).
func (o *OPLMArt) Fetch(startup string, t ArtType) (string, error) {
	if err := o.open(); err != nil {
		return "", err
	}
	n := disc.NormalizeSerial(startup)
	f, ok := o.byKey[disc.StartupFromSerial(n)+"_"+t.oplm]
	if !ok {
		return "", ErrNoArt
	}
	o.limiter <- struct{}{}
	b, err := o.zip.ReadMember(f, 8<<20)
	<-o.limiter
	if err != nil {
		return "", err
	}
	return savePicture(b, t, startup)
}

// ErrNoArt: the source has no picture of this type for the game.
var ErrNoArt = errors.New("no picture in the art database")

// FetchArt fills the cache with the requested types for a game: the front cover
// from the cover URL first, everything else (and missing covers) from the OPL
// Manager backup. It returns the types now cached and a note per failure.
func FetchArt(oplm *OPLMArt, coverURL, startup string, keys []string) (have []string, problems []string) {
	for _, k := range keys {
		t, ok := ArtTypeByKey(k)
		if !ok {
			continue
		}
		if CachedArt(startup, k) != "" {
			have = append(have, k)
			continue
		}
		var err error
		if k == "COV" && coverURL != "" {
			if _, err = DownloadCover(coverURL, startup); err == nil {
				have = append(have, k)
				continue
			}
		}
		if oplm != nil {
			if _, err = oplm.Fetch(startup, t); err == nil {
				have = append(have, k)
				continue
			}
		}
		if err != nil && !errors.Is(err, ErrNoArt) {
			problems = append(problems, t.Label+": "+err.Error())
		}
	}
	return have, problems
}
