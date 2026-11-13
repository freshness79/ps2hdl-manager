package gamedb

import (
	"bufio"
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OPL compatibility modes (include/iosupport.h, OPL 1.x).
var ModeNames = map[int]string{
	1: "Accurate Reads",
	2: "Synchronous Mode",
	3: "Unhook Syscalls",
	4: "Skip Videos",
	5: "Emulate DVD-DL",
	6: "Disable IGR",
}

// GameInfo is everything we know about a title beyond its name.
type GameInfo struct {
	Genre       string
	Players     int // 0 = unknown
	Developer   string
	Publisher   string
	Release     string // MM-DD-YYYY, as OPL shows it
	Rating      string // "0".."5"
	Description string
	Vmode       string // OPL theme value: ntsc, pal, multi
	Aspect      string // s (4:3), w, w1, w2 (16:9)
	Scan        string // 480i, 480p, 240p1…
	ModesMask   int    // recommended OPL compatibility modes (bitmask)
	CompatKnown bool   // the compatibility DB has an entry for this ID
	CompatSrc   bool   // the compatibility DB was reachable
}

// Modes returns the recommended mode numbers.
func (g *GameInfo) Modes() []int { return maskToModes(g.ModesMask) }

func maskToModes(m int) []int {
	var out []int
	for i := 1; i <= 8; i++ {
		if m&(1<<(i-1)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// FlagsString renders a mask in hdl_dump syntax: "+2+3+6".
func FlagsString(m int) string {
	var sb strings.Builder
	for _, n := range maskToModes(m) {
		sb.WriteString("+" + strconv.Itoa(n))
	}
	return sb.String()
}

// FlagsToMask parses "+1+2" / "1+2".
func FlagsToMask(f string) int {
	m := 0
	for _, p := range strings.Split(strings.TrimPrefix(f, "+"), "+") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 1 && n <= 8 {
			m |= 1 << (n - 1)
		}
	}
	return m
}

func ModesDescription(m int) string {
	var parts []string
	for _, n := range maskToModes(m) {
		parts = append(parts, fmt.Sprintf("%d %s", n, ModeNames[n]))
	}
	return strings.Join(parts, ", ")
}

func (g *GameInfo) MultiplayerText() string {
	switch {
	case g == nil || g.Players == 0:
		return "—"
	case g.Players > 1:
		return fmt.Sprintf("Yes · %dP", g.Players)
	}
	return "No"
}

// ---------------------------------------------------------------- validation

// The OPL info page shows some keys as theme images (players/2, vmode/pal…):
// only values a theme can have are kept, since some source files carry text in
// the wrong key.
var (
	reRelease = regexp.MustCompile(`^(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])-(19|20)\d\d$`)
	reVmode   = regexp.MustCompile(`^(ntsc|pal|multi)$`)
	reAspect  = regexp.MustCompile(`^(s|w|w1|w2)$`)
	reScan    = regexp.MustCompile(`^(240p|480i|480p|576i|576p|720p|1080i|1080p)[1-9]?$`)
	reRating  = regexp.MustCompile(`^[0-5]$`)
	rePlayers = regexp.MustCompile(`^[1-8]$`)
)

// themeValue extracts "pal" from "vmode/pal" (or "pal") if it is valid.
func themeValue(v, prefix string, re *regexp.Regexp) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, prefix+"/")
	if re.MatchString(v) {
		return v
	}
	return ""
}

// cleanText is a single-line text value of reasonable length ("" otherwise).
func cleanText(v string, max int) string {
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > max {
		return ""
	}
	return v
}

// isoToOPLDate turns 2004-10-26 into 10-26-2004 ("" if not a full date).
func isoToOPLDate(d string) string {
	p := strings.Split(strings.TrimSpace(d), "-")
	if len(p) != 3 || len(p[0]) != 4 {
		return ""
	}
	out := p[1] + "-" + p[2] + "-" + p[0]
	if !reRelease.MatchString(out) {
		return ""
	}
	return out
}

func (g *GameInfo) fromCFG(kv map[string]string) {
	g.Genre = cleanText(kv["Genre"], 40)
	g.Developer = cleanText(kv["Developer"], 64)
	g.Publisher = cleanText(kv["Publisher"], 64)
	if r := strings.TrimSpace(kv["Release"]); reRelease.MatchString(r) {
		g.Release = r
	}
	g.Rating = themeValue(kv["Rating"], "rating", reRating)
	if g.Rating == "" {
		g.Rating = themeValue(kv["RatingText"], "rating", reRating)
	}
	g.Description = strings.TrimSpace(strings.ReplaceAll(kv["Description"], "\n", " "))
	p := themeValue(kv["Players"], "players", rePlayers)
	if p == "" {
		p = themeValue(kv["PlayersText"], "players", rePlayers)
	}
	g.Players, _ = strconv.Atoi(p)
	g.Vmode = themeValue(kv["Vmode"], "vmode", reVmode)
	g.Aspect = themeValue(kv["Aspect"], "aspect", reAspect)
	g.Scan = themeValue(kv["Scan"], "scan", reScan)
}

// fromMeta fills what the OPL info file lacked from the title database.
func (g *GameInfo) fromMeta(m Meta) {
	fill := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	fill(&g.Genre, cleanText(m.Genre, 40))
	fill(&g.Developer, cleanText(m.Developer, 64))
	fill(&g.Publisher, cleanText(m.Publisher, 64))
	fill(&g.Release, isoToOPLDate(m.Released))
}

// VmodeText, AspectText and ScanText are the captions OPL shows next to the images.
func VmodeText(v string) string {
	switch v {
	case "ntsc":
		return "NTSC"
	case "pal":
		return "PAL"
	case "multi":
		return "NTSC/PAL"
	}
	return ""
}

func AspectText(v string) string {
	switch v {
	case "s":
		return "4:3"
	case "w", "w1", "w2":
		return "16:9"
	}
	return ""
}

func ScanText(v string) string { return strings.TrimRight(v, "123456789") }

// ---------------------------------------------------------------- store

type InfoStore struct {
	// sources (see config.DefaultInfoURL…): "" disables one; {STARTUP} = SLUS_900.02
	infoURL, compatListURL, compatCfgURL string

	mu      sync.Mutex
	m       map[string]*GameInfo
	compat  map[string]bool // IDs present in the compatibility DB
	compErr error
	client  *http.Client
	db      *TitleDB
}

func NewInfoStore(db *TitleDB) *InfoStore {
	return &InfoStore{m: map[string]*GameInfo{}, db: db, client: &http.Client{Timeout: 20 * time.Second},
		infoURL: config.DefaultInfoURL, compatListURL: config.DefaultCompatListURL, compatCfgURL: config.DefaultCompatCfgURL}
}

// SetSources changes the online sources ("" = off); loaded info is dropped
// when they change.
func (s *InfoStore) SetSources(info, compatList, compatCfg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infoURL == info && s.compatListURL == compatList && s.compatCfgURL == compatCfg {
		return
	}
	s.infoURL, s.compatListURL, s.compatCfgURL = info, compatList, compatCfg
	s.m = map[string]*GameInfo{}
	s.compat, s.compErr = nil, nil
}

func (s *InfoStore) sources() (info, list, cfg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.infoURL, s.compatListURL, s.compatCfgURL
}

// fillURL puts a game in a source URL: {ID} = SLUS-90002, {STARTUP} = SLUS_900.02.
func fillURL(tmpl, serial string) string {
	n := disc.NormalizeSerial(serial)
	id := n
	if len(n) > 4 {
		id = n[:4] + "-" + n[4:]
	}
	return strings.NewReplacer("{ID}", id, "{STARTUP}", disc.StartupFromSerial(n)).Replace(tmpl)
}

// cacheName keeps the files of different sources apart.
func cacheName(source, name string) string {
	if strings.HasPrefix(source, "https://raw.githubusercontent.com/Tom-Bruise/") ||
		strings.HasPrefix(source, "https://raw.githubusercontent.com/GDX-X/") {
		return name // default sources: names of earlier versions
	}
	h := crc32.ChecksumIEEE([]byte(source))
	return fmt.Sprintf("%08x-%s", h, name)
}

func infoCacheDir() string {
	d := filepath.Join(config.Dir(), "info")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Cached returns already-loaded info (no network).
func (s *InfoStore) Cached(id string) *GameInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[disc.NormalizeSerial(id)]
}

// Put stores info for id (used by tests and previews; no network).
func (s *InfoStore) Put(id string, gi *GameInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[disc.NormalizeSerial(id)] = gi
}

// Get returns the info for id, loading it from disk cache or the network.
func (s *InfoStore) Get(id string) (*GameInfo, error) {
	n := disc.NormalizeSerial(id)
	if n == "" {
		return &GameInfo{}, nil
	}
	if g := s.Cached(n); g != nil {
		return g, nil
	}
	st := disc.StartupFromSerial(n)
	gi := &GameInfo{}
	infoURL, compatListURL, compatCfgURL := s.sources()

	// game info (players, genre, …)
	if infoURL != "" {
		if kv, err := s.fetchCached(fillURL(infoURL, n), cacheName(infoURL, st+".cfg")); err == nil {
			gi.fromCFG(kv)
		}
	}
	if s.db != nil {
		gi.fromMeta(s.db.Meta(n))
	}
	if gi.Vmode == "" {
		switch Region(st) {
		case "PAL":
			gi.Vmode = "pal"
		case "NTSC-U", "NTSC-J":
			gi.Vmode = "ntsc"
		}
	}

	// compatibility modes
	if compatListURL == "" || compatCfgURL == "" {
		// compatibility database off
	} else if err := s.loadCompatList(compatListURL); err == nil {
		gi.CompatSrc = true
		s.mu.Lock()
		known := s.compat[n]
		s.mu.Unlock()
		if known {
			gi.CompatKnown = true
			if kv, err := s.fetchCached(fillURL(compatCfgURL, n), cacheName(compatCfgURL, "compat-"+st+".cfg")); err == nil {
				gi.ModesMask, _ = strconv.Atoi(kv["$Compatibility"])
				if gi.ModesMask == 0 && kv["Modes"] != "" {
					gi.ModesMask = FlagsToMask(kv["Modes"])
				}
			}
		}
	}
	s.mu.Lock()
	s.m[n] = gi
	s.mu.Unlock()
	return gi, nil
}

// fetchCached downloads a CFG once and keeps it on disk ("" file = not found online).
func (s *InfoStore) fetchCached(url, name string) (map[string]string, error) {
	p := filepath.Join(infoCacheDir(), name)
	if b, err := os.ReadFile(p); err == nil {
		if len(b) == 0 {
			return nil, fmt.Errorf("not found")
		}
		return ParseCFG(string(b)), nil
	}
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err // network error: do not cache
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		_ = os.WriteFile(p, nil, 0o644)
		return nil, fmt.Errorf("not found")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(p, b, 0o644)
	return ParseCFG(string(b)), nil
}

func (s *InfoStore) loadCompatList(compatListURL string) error {
	s.mu.Lock()
	if s.compat != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	p := filepath.Join(infoCacheDir(), cacheName(compatListURL, "compat-list.txt"))
	var data []byte
	if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) < 7*24*time.Hour {
		data, _ = os.ReadFile(p)
	}
	if data == nil {
		resp, err := s.client.Get(compatListURL)
		if err == nil && resp.StatusCode == 200 {
			data, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			_ = os.WriteFile(p, data, 0o644)
		} else {
			if resp != nil {
				resp.Body.Close()
			}
			data, _ = os.ReadFile(p) // stale cache is better than nothing
			if data == nil {
				if err == nil {
					err = fmt.Errorf("compatibility list: HTTP %d", resp.StatusCode)
				}
				return err
			}
		}
	}
	m := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		if n := disc.NormalizeSerial(strings.Fields(l + " x")[0]); n != "" {
			m[n] = true
		}
	}
	s.mu.Lock()
	s.compat = m
	s.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------- OPL CFG files

// ParseCFG parses OPL "key=value" files.
func ParseCFG(s string) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if i := strings.IndexByte(l, '='); i > 0 {
			kv[strings.TrimSpace(l[:i])] = strings.TrimSpace(l[i+1:])
		}
	}
	return kv
}

// FormatCFG writes keys in a stable order (CfgVersion and Title first).
func FormatCFG(kv map[string]string) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		switch k {
		case "CfgVersion":
			return 0
		case "Title":
			return 1
		}
		if strings.HasPrefix(k, "$") {
			return 3
		}
		return 2
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if rank(keys[i]) != rank(keys[j]) {
			return rank(keys[i]) < rank(keys[j])
		}
		return keys[i] < keys[j]
	})
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k + "=" + kv[k] + "\r\n")
	}
	return sb.String()
}

// MergeGameCFG adds game info to an OPL CFG and (setModes) sets the
// compatibility modes. Values already there are kept unless they are invalid
// (a theme image value OPL cannot show, a malformed date); the caption keys
// (PlayersText, VmodeText…) always follow their value.
func MergeGameCFG(existing map[string]string, title string, gi *GameInfo, setModes bool) map[string]string {
	kv := map[string]string{}
	for k, v := range existing {
		kv[k] = v
	}
	add := func(k, v string) {
		if v != "" && strings.TrimSpace(kv[k]) == "" {
			kv[k] = v
		}
	}
	// theme keys: keep a valid value, else replace (or drop) it
	theme := func(key, prefix string, re *regexp.Regexp, v string, text func(string) string) {
		cur := themeValue(kv[key], prefix, re)
		if cur == "" {
			cur = v
		}
		if cur == "" {
			delete(kv, key)
			delete(kv, key+"Text")
			return
		}
		kv[key] = prefix + "/" + cur
		kv[key+"Text"] = text(cur)
	}
	ident := func(s string) string { return s }
	add("CfgVersion", "8")
	add("Title", title)
	if r, ok := kv["Release"]; ok && !reRelease.MatchString(strings.TrimSpace(r)) {
		delete(kv, "Release")
	}
	if gi == nil {
		gi = &GameInfo{}
	}
	add("Genre", gi.Genre)
	add("Developer", gi.Developer)
	add("Publisher", gi.Publisher)
	add("Release", gi.Release)
	add("Description", gi.Description)
	players := ""
	if gi.Players > 0 {
		players = strconv.Itoa(gi.Players)
	}
	theme("Players", "players", rePlayers, players, ident)
	theme("Rating", "rating", reRating, gi.Rating, ident)
	theme("Vmode", "vmode", reVmode, gi.Vmode, VmodeText)
	theme("Aspect", "aspect", reAspect, gi.Aspect, AspectText)
	theme("Scan", "scan", reScan, gi.Scan, ScanText)
	if setModes && gi.ModesMask != 0 {
		kv["$Compatibility"] = strconv.Itoa(gi.ModesMask)
		kv["Modes"] = strings.TrimPrefix(FlagsString(gi.ModesMask), "+")
	}
	return kv
}
