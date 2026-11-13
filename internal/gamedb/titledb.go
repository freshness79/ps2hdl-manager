package gamedb

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TitleDB is the online serial → title database, with a local cache.
type TitleDB struct {
	mu      sync.RWMutex
	titles  map[string]string // key: normalised serial (SLUS90001)
	meta    map[string]Meta
	Updated time.Time
}

// Meta is what PS2.data.json knows about a game besides its title.
type Meta struct {
	Genre     string `json:"genre,omitempty"`
	Developer string `json:"developer,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Released  string `json:"release_date,omitempty"` // as in the source: YYYY-MM-DD
	Region    string `json:"region,omitempty"`
}

func NewTitleDB() *TitleDB { return &TitleDB{titles: map[string]string{}, meta: map[string]Meta{}} }

// Genre returns the first genre known for id (from PS2.data.json).
func (db *TitleDB) Genre(id string) string { return db.Meta(id).Genre }

// Meta returns the metadata known for id (zero value if none).
func (db *TitleDB) Meta(id string) Meta {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.meta[disc.NormalizeSerial(id)]
}

func dbCachePath() string { return filepath.Join(config.Dir(), "titles-cache.json") }

func (db *TitleDB) Count() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.titles)
}

// Lookup finds the title of a startup ID (any format).
func (db *TitleDB) Lookup(id string) (string, bool) {
	k := disc.NormalizeSerial(id)
	if k == "" {
		return "", false
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	t, ok := db.titles[k]
	return t, ok
}

// LoadCache loads the database from the local cache.
func (db *TitleDB) LoadCache() error {
	b, err := os.ReadFile(dbCachePath())
	if err != nil {
		return err
	}
	st, _ := os.Stat(dbCachePath())
	if err := db.parse(b); err != nil {
		return err
	}
	if st != nil {
		db.Updated = st.ModTime()
	}
	return nil
}

// Download fetches the online database and updates the cache.
func (db *TitleDB) Download(url string) error {
	client := &http.Client{Timeout: 90 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "ps2hdl-manager/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download DB: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := db.parse(b); err != nil {
		return err
	}
	db.Updated = time.Now()
	return db.saveCache()
}

func (db *TitleDB) saveCache() error {
	db.mu.RLock()
	type entry struct {
		Title string `json:"title"`
		Meta
	}
	all := map[string]entry{}
	for k, t := range db.titles {
		all[k] = entry{t, db.meta[k]}
	}
	b, err := json.Marshal(all)
	db.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(dbCachePath(), b, 0o644)
}

// parse accepts several formats:
//   - JSON {"SLUS-90001": "Title"} or {"SLUS-90001": {"title": "..."}}
//   - JSON array [{"serial": "...", "title": "..."}]
//   - text "SLUS_900.01 Title" / "SLUS-90001;Title" / TSV
func (db *TitleDB) parse(b []byte) error {
	m := map[string]string{}
	metas := map[string]Meta{}
	trim := bytes.TrimSpace(b)
	switch {
	case len(trim) > 0 && trim[0] == '{':
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(trim, &raw); err != nil {
			return err
		}
		for k, v := range raw {
			var s string
			if json.Unmarshal(v, &s) != nil {
				var obj map[string]any
				var arr []string
				if json.Unmarshal(v, &obj) == nil {
					s = pickTitle(obj)
					if md := pickMeta(obj); md != (Meta{}) {
						if k := disc.NormalizeSerial(k); k != "" {
							metas[k] = md
						}
					}
				} else if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
					s = arr[0]
				}
			}
			addTitle(m, k, s)
		}
	case len(trim) > 0 && trim[0] == '[':
		var arr []map[string]any
		if err := json.Unmarshal(trim, &arr); err != nil {
			return err
		}
		for _, o := range arr {
			ser, _ := o["serial"].(string)
			if ser == "" {
				ser, _ = o["id"].(string)
			}
			addTitle(m, ser, pickTitle(o))
		}
	default:
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			loc := disc.SerialRe.FindStringIndex(strings.ToUpper(line))
			if loc == nil {
				continue
			}
			ser := line[loc[0]:loc[1]]
			title := strings.TrimSpace(line[:loc[0]] + " " + line[loc[1]:])
			title = strings.Trim(title, " \t;,=|")
			addTitle(m, ser, title)
		}
	}
	if len(m) == 0 {
		return fmt.Errorf("database empty or unknown format")
	}
	db.mu.Lock()
	db.titles = m
	db.meta = metas
	db.mu.Unlock()
	return nil
}

func pickTitle(o map[string]any) string {
	for _, k := range []string{"title", "name", "Title", "Name"} {
		if s, ok := o[k].(string); ok {
			return s
		}
	}
	return ""
}

func addTitle(m map[string]string, serial, title string) {
	k := disc.NormalizeSerial(serial)
	title = strings.TrimSpace(title)
	if k == "" || title == "" {
		return
	}
	if _, exists := m[k]; !exists {
		m[k] = title
	}
}

// CleanTitle makes a title fit for an HDL partition (ASCII, at most 64 characters).
func CleanTitle(t string) string {
	r := strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"", "–", "-", "—", "-", "®", "", "™", "", ":", " -")
	t = r.Replace(t)
	var sb strings.Builder
	for _, c := range t {
		switch {
		case c == '"':
			sb.WriteRune('\'')
		case c >= 32 && c < 127:
			sb.WriteRune(c)
		default:
			sb.WriteRune(asciiFold(c))
		}
	}
	out := strings.Join(strings.Fields(sb.String()), " ")
	if len(out) > 64 {
		out = strings.TrimSpace(out[:64])
	}
	return out
}

func asciiFold(c rune) rune {
	const from = "àáâãäåèéêëìíîïòóôõöùúûüñçÀÁÂÃÄÅÈÉÊËÌÍÎÏÒÓÔÕÖÙÚÛÜÑÇ"
	const to = "aaaaaaeeeeiiiiooooouuuuncAAAAAAEEEEIIIIOOOOOUUUUNC"
	fr := []rune(from)
	for i, r := range fr {
		if r == c {
			return rune(to[i])
		}
	}
	return '_'
}

func pickGenre(o map[string]any) string {
	var g string
	switch v := o["genre"].(type) {
	case string:
		g = v
	case []any:
		if len(v) > 0 {
			g, _ = v[0].(string)
		}
	}
	return strings.TrimRight(strings.TrimSpace(g), ".")
}

// pickMeta reads the metadata fields of a PS2.data.json entry (strings or
// lists of strings; the first item is used).
func pickMeta(o map[string]any) Meta {
	return Meta{
		Genre:     pickGenre(o),
		Developer: firstString(o["developer"]),
		Publisher: firstString(o["publisher"]),
		Released:  firstString(o["release_date"]),
		Region:    firstString(o["region"]),
	}
}

func firstString(v any) string {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case []any:
		if len(x) > 0 {
			s, _ = x[0].(string)
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return ""
	}
	return s
}
