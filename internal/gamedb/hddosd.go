package gamedb

// HDD-OSD (PS2 "Browser 2.0") presentation of installed games: the 3D icon
// (list.ico) and the icon.sys text that gives the two title lines. hdl_dump
// reads both from its working directory when installing (inject) or when
// rewriting a partition header (modify_header).

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/freshness79/ps2hdl-manager/internal/buildinfo"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
)

// The icon pack for HDD-OSD (config.DefaultIconIndex, MIT licence): Icons.ini
// maps game IDs to icon files, which live in ICNS/ next to it.
const (
	maxListIco = 0x3F800 // largest list.ico hdl_dump accepts
	maxIconSys = 0x200   // largest icon.sys text hdl_dump accepts
)

// IconDB finds PS2 3D icons by game ID.
type IconDB struct {
	IndexURL, BaseURL string

	mu      sync.Mutex
	loaded  bool
	failed  time.Time // last failed load: not retried for a while
	failErr error
	byID    map[string]string // "SLUS_900.02" → "Sample_Game_Eta.ico"
}

// NewIconDB uses the icon pack whose Icons.ini is at index (icons in ICNS/ beside it).
func NewIconDB(index string) *IconDB {
	base := index
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[:i+1]
	}
	return &IconDB{IndexURL: index, BaseURL: base + "ICNS/"}
}

func iconDir() string {
	d := filepath.Join(config.Dir(), "hddosd")
	_ = os.MkdirAll(filepath.Join(d, "icons"), 0o755)
	return d
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

func httpGet(u string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ps2hdl-manager/"+buildinfo.Version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrNoArt
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// load reads the index (downloaded at most once a week, cached on disk).
func (db *IconDB) load() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.loaded {
		return nil
	}
	if time.Since(db.failed) < 5*time.Minute {
		return db.failErr
	}
	cache := filepath.Join(iconDir(), "Icons.ini")
	st, err := os.Stat(cache)
	if err != nil || time.Since(st.ModTime()) > 7*24*time.Hour {
		if b, derr := httpGet(db.IndexURL, 16<<20); derr == nil && len(parseIconIndex(b)) > 0 {
			_ = os.WriteFile(cache, b, 0o644)
		} else if err != nil {
			if derr == nil {
				derr = errors.New("no icons listed")
			}
			db.failed, db.failErr = time.Now(), fmt.Errorf("HDD-OSD icon index: %w", derr)
			return db.failErr
		}
	}
	b, err := os.ReadFile(cache)
	if err != nil {
		return err
	}
	db.byID = parseIconIndex(b)
	db.loaded = true
	return nil
}

func parseIconIndex(b []byte) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "[") || strings.HasPrefix(k, ";") {
			continue
		}
		if n := disc.NormalizeSerial(k); n != "" && strings.HasSuffix(strings.ToLower(v), ".ico") && !strings.ContainsAny(v, `/\`) {
			m[n] = strings.TrimSpace(v)
		}
	}
	return m
}

// Count is the number of game IDs with an icon (after loading).
func (db *IconDB) Count() int {
	if db.load() != nil {
		return 0
	}
	return len(db.byID)
}

// Icon returns the cached list.ico for a game, downloading it if needed;
// ErrNoArt when the pack has no icon for it.
func (db *IconDB) Icon(startup string) (string, error) {
	if err := db.load(); err != nil {
		return "", err
	}
	name, ok := db.byID[disc.NormalizeSerial(startup)]
	if !ok {
		return "", ErrNoArt
	}
	p := filepath.Join(iconDir(), "icons", name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	b, err := httpGet(db.BaseURL+url.PathEscape(name), maxListIco+1)
	if err != nil {
		return "", fmt.Errorf("icon %s: %w", name, err)
	}
	if err := ValidatePS2Icon(b); err != nil {
		return "", fmt.Errorf("icon %s: %w", name, err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// ValidatePS2Icon checks the PS2 3D icon header (magic 0x00010000, plausible
// vertex count) and the size limit of the partition header.
func ValidatePS2Icon(b []byte) error {
	if len(b) < 20 || binary.LittleEndian.Uint32(b[0:4]) != 0x00010000 {
		return errors.New("not a PS2 3D icon")
	}
	if len(b) > maxListIco {
		return fmt.Errorf("%d bytes: larger than the %d bytes the partition header holds", len(b), maxListIco)
	}
	if v := binary.LittleEndian.Uint32(b[16:20]); v == 0 || v%3 != 0 || v > 30000 {
		return fmt.Errorf("implausible vertex count %d", v)
	}
	return nil
}

// Region returns the market of a game ID ("NTSC-U", "PAL", "NTSC-J"), "" if unknown.
func Region(startup string) string {
	n := disc.NormalizeSerial(startup)
	if len(n) < 4 {
		return ""
	}
	switch n[:4] {
	case "SLUS", "SCUS", "SLUD", "SCUD", "PBPX":
		return "NTSC-U"
	case "SLES", "SCES", "SCED", "SLED", "SCCS":
		return "PAL"
	case "SLPS", "SLPM", "SCPS", "SCPM", "SCAJ", "SLAJ", "SCKA", "SLKA", "PAPX", "CPCS", "ALCH", "KOEI", "NAMJ":
		return "NTSC-J"
	}
	return ""
}

// IconSys builds the HDD-format icon.sys ("PS2X" text) for a game: the first
// line is the title, the second the product code and market, as HDD-OSD shows
// them under the icon.
func IconSys(title, startup string) []byte {
	clean := func(s string, n int) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r == '\n' || r == '\r' || r == '=':
				b.WriteRune(' ')
			case r < 0x80 && unicode.IsPrint(r):
				b.WriteRune(r)
			default:
				b.WriteRune(osdFold(r))
			}
		}
		out := strings.Join(strings.Fields(b.String()), " ")
		if len(out) > n {
			out = strings.TrimSpace(out[:n])
		}
		return out
	}
	code := ""
	if n := disc.NormalizeSerial(startup); n != "" {
		code = n[:4] + "-" + n[4:]
		if r := Region(startup); r != "" {
			code += " (" + r + ")"
		}
	}
	// same layout hdl_dump itself writes ("key = value")
	s := "PS2X\n" +
		"title0 = " + clean(title, 64) + "\n" +
		"title1 = " + clean(code, 32) + "\n" +
		"bgcola = 64\nbgcol0 = 22,47,92\nbgcol1 = 3,10,28\nbgcol2 = 3,10,28\nbgcol3 = 22,47,92\n" +
		"lightdir0 = 0.5000,0.5000,0.5000\nlightdir1 = 0.0000,-0.4000,-1.0000\nlightdir2 = 0.5000,-0.5000,0.5000\n" +
		"lightcolamb = 31,31,31\nlightcol0 = 62,62,55\nlightcol1 = 33,42,64\nlightcol2 = 18,18,49\n" +
		"uninstallmes0 =This will delete the game.\nuninstallmes1 =\nuninstallmes2 =\n"
	if len(s) > maxIconSys {
		s = s[:maxIconSys]
	}
	return []byte(s)
}

// osdFold maps common accented letters and typographic quotes to ASCII (HDD-OSD text is ASCII here).
func osdFold(r rune) rune {
	const from = "àáâãäåèéêëìíîïòóôõöùúûüýÿñçÀÁÂÃÄÅÈÉÊËÌÍÎÏÒÓÔÕÖÙÚÛÜÝÑÇ’‘“”–—"
	const to = "aaaaaaeeeeiiiiooooouuuuyyncAAAAAAEEEEIIIIOOOOOUUUUYNC''\"\"--"
	i := 0
	for _, f := range from {
		if f == r {
			return rune(to[i])
		}
		i++
	}
	return '?'
}

// HeaderDir prepares a folder with the files hdl_dump puts in a game's partition
// header: icon.sys always, list.ico when an icon is available (otherwise the
// installed icon is kept, or HD Loader's default one at install time).
func HeaderDir(parent, title, startup, icon string) (string, error) {
	dir, err := os.MkdirTemp(parent, "hdr-")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "icon.sys"), IconSys(title, startup), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if icon != "" {
		b, err := os.ReadFile(icon)
		if err == nil {
			err = ValidatePS2Icon(b)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "list.ico"), b, 0o644)
		}
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}
