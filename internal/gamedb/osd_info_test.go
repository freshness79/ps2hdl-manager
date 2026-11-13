package gamedb

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
}

func TestTitleDBMeta(t *testing.T) {
	isolate(t)
	db := NewTitleDB()
	js := `{"SLUS-90002":{"title":"Sample Game Eta: Extended","developer":["Example Studio","x"],"publisher":"Example Publisher",
	"release_date":"2004-10-26","region":"NTSC-U","genre":["Action"]},"SLES-50001":{"title":"Other","release_date":"2001"}}`
	if err := db.parse([]byte(js)); err != nil {
		t.Fatal(err)
	}
	m := db.Meta("SLUS_900.02")
	if m.Developer != "Example Studio" || m.Publisher != "Example Publisher" || m.Released != "2004-10-26" || m.Genre != "Action" || m.Region != "NTSC-U" {
		t.Fatalf("%+v", m)
	}
	// the cache keeps the metadata
	if err := db.saveCache(); err != nil {
		t.Fatal(err)
	}
	db2 := NewTitleDB()
	if err := db2.LoadCache(); err != nil {
		t.Fatal(err)
	}
	if db2.Meta("SLUS-90002") != m {
		t.Fatalf("cache lost metadata: %+v", db2.Meta("SLUS-90002"))
	}
	gi := &GameInfo{}
	gi.fromMeta(db2.Meta("SLUS-90002"))
	if gi.Release != "10-26-2004" || gi.Publisher != "Example Publisher" {
		t.Fatalf("%+v", gi)
	}
	gi = &GameInfo{}
	gi.fromMeta(db2.Meta("SLES-50001"))
	if gi.Release != "" {
		t.Fatal("year-only date used:", gi.Release)
	}
}

func TestInfoFromCFGValidates(t *testing.T) {
	gi := &GameInfo{}
	gi.fromCFG(ParseCFG("Genre=Action\nPlayers=players/2\nVmode=Sample Game Eta is an open world game\n" +
		"Aspect=aspect/w\nScan=scan/480p1\nRating=rating/9\nRatingText=4\nRelease=13-40-2004\nDeveloper=Example Studio\n"))
	if gi.Players != 2 || gi.Vmode != "" || gi.Aspect != "w" || gi.Scan != "480p1" || gi.Rating != "4" || gi.Release != "" {
		t.Fatalf("%+v", gi)
	}
}

func TestMergeGameCFG(t *testing.T) {
	gi := &GameInfo{Genre: "Action", Players: 2, Developer: "Dev", Publisher: "Pub", Release: "10-26-2004",
		Rating: "4", Description: "Desc", Vmode: "ntsc", Aspect: "s", Scan: "480i", ModesMask: 1 | 4}
	old := map[string]string{
		"Title": "Mine", "Genre": "Racing", // user values are kept
		"Vmode": "some description text", "VmodeText": "garbage", // invalid: replaced
		"Players":        "players/1", // valid: kept, text added
		"Release":        "2004",      // invalid date: replaced
		"$Compatibility": "2", "Modes": "2",
	}
	kv := MergeGameCFG(old, "Game", gi, false)
	want := map[string]string{
		"CfgVersion": "8", "Title": "Mine", "Genre": "Racing", "Developer": "Dev", "Publisher": "Pub",
		"Release": "10-26-2004", "Description": "Desc",
		"Players": "players/1", "PlayersText": "1", "Rating": "rating/4", "RatingText": "4",
		"Vmode": "vmode/ntsc", "VmodeText": "NTSC", "Aspect": "aspect/s", "AspectText": "4:3",
		"Scan": "scan/480i", "ScanText": "480i", "$Compatibility": "2", "Modes": "2",
	}
	for k, v := range want {
		if kv[k] != v {
			t.Errorf("%s = %q, want %q", k, kv[k], v)
		}
	}
	if len(kv) != len(want) {
		t.Errorf("extra keys: %v", kv)
	}
	if kv = MergeGameCFG(old, "Game", gi, true); kv["$Compatibility"] != "5" || kv["Modes"] != "1+3" {
		t.Errorf("modes: %v %v", kv["$Compatibility"], kv["Modes"])
	}
	// nothing known and an invalid value: dropped, not kept as garbage
	kv = MergeGameCFG(map[string]string{"Scan": "scan/progressive"}, "Game", nil, false)
	if _, ok := kv["Scan"]; ok || kv["Title"] != "Game" {
		t.Errorf("%v", kv)
	}
	// stable: merging again changes nothing
	a := MergeGameCFG(old, "Game", gi, true)
	if FormatCFG(MergeGameCFG(a, "Game", gi, true)) != FormatCFG(a) {
		t.Error("merge not idempotent")
	}
}

func icon(tag byte) []byte {
	b := make([]byte, 2048)
	binary.LittleEndian.PutUint32(b, 0x00010000)
	binary.LittleEndian.PutUint32(b[16:], 9)
	b[50] = tag
	return b
}

func TestIconSysAndHeaderDir(t *testing.T) {
	s := string(IconSys("Élan: “Edición” = Café\nX", "SCES_900.03"))
	if !strings.HasPrefix(s, "PS2X\n") || len(s) > maxIconSys {
		t.Fatal(s)
	}
	if !strings.Contains(s, "\ntitle0 = Elan: \"Edicion\" Cafe X\n") || !strings.Contains(s, "\ntitle1 = SCES-90003 (PAL)\n") {
		t.Fatal(s)
	}
	if s := string(IconSys(strings.Repeat("A", 200), "")); !strings.Contains(s, "title0 = "+strings.Repeat("A", 64)+"\n") || !strings.Contains(s, "title1 = \n") {
		t.Fatal(s)
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.ico")
	_ = os.WriteFile(bad, []byte("not an icon at all, just text....."), 0o644)
	if _, err := HeaderDir(dir, "x", "SLUS_900.02", bad); err == nil {
		t.Fatal("invalid icon accepted")
	}
	good := filepath.Join(dir, "good.ico")
	_ = os.WriteFile(good, icon(1), 0o644)
	h, err := HeaderDir(dir, "x", "SLUS_900.02", good)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h, "list.ico")); len(b) != 2048 {
		t.Fatal("list.ico missing")
	}
	h2, _ := HeaderDir(dir, "x", "SLUS_900.02", "")
	if _, err := os.Stat(filepath.Join(h2, "list.ico")); err == nil {
		t.Fatal("list.ico written without an icon")
	}
	if Region("SLPM_123.45") != "NTSC-J" || Region("SLUS_900.02") != "NTSC-U" || Region("SLES_123.45") != "PAL" {
		t.Fatal("regions")
	}
}

func TestIconDB(t *testing.T) {
	isolate(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/pack/Icons.ini":
			w.Write([]byte("[ICONS]\r\nSLUS_900.02=Sample Game Eta.ico\r\nSLES_123.45=../evil.ico\r\nSCES_900.03=broken.ico\r\n"))
		case "/pack/ICNS/Sample Game Eta.ico":
			w.Write(icon(7))
		case "/pack/ICNS/broken.ico":
			w.Write([]byte("<html>not found</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	db := NewIconDB(srv.URL + "/pack/Icons.ini")
	if db.BaseURL != srv.URL+"/pack/ICNS/" {
		t.Fatal(db.BaseURL)
	}
	if db.Count() != 2 {
		t.Fatal("count", db.Count())
	}
	p, err := db.Icon("SLUS-90002")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); len(b) != 2048 || b[50] != 7 {
		t.Fatal("icon content")
	}
	n := hits
	if _, err := db.Icon("SLUS_900.02"); err != nil || hits != n {
		t.Fatal("icon not cached", err)
	}
	if _, err := db.Icon("SCES_900.03"); err == nil || err == ErrNoArt {
		t.Fatal("invalid icon accepted:", err)
	}
	if _, err := db.Icon("SLES_123.45"); err != ErrNoArt {
		t.Fatal("path in index accepted:", err)
	}
	// unreachable index: error, and no retry storm
	dead := NewIconDB("http://127.0.0.1:1/Icons.ini")
	isolate(t)
	if _, err := dead.Icon("SLUS_900.02"); err == nil {
		t.Fatal("no error")
	}
}

func TestArtNames(t *testing.T) {
	names := AllArtNames("SLUS-90002")
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("SLUS_900.02_COV.png") || !has("SLUS_900.02_COV.jpg") || !has("SLUS_900.02_SCR2.png") || !has("SLUS_900.02_LGO.png") {
		t.Fatal(names)
	}
	if strings.Join(DefaultArtKeys(), ",") != "COV,ICO,BG,SCR,SCR2" {
		t.Fatal(DefaultArtKeys())
	}
}

func TestInfoStoreCustomSources(t *testing.T) {
	isolate(t)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/info/SLUS-90002.cfg":
			w.Write([]byte("Genre=Custom\nPlayers=players/4\n"))
		case "/list.txt":
			w.Write([]byte("SLUS_900.02\n"))
		case "/modes/SLUS_900.02.cfg":
			w.Write([]byte("$Compatibility=8\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := NewInfoStore(nil)
	s.SetSources(srv.URL+"/info/{ID}.cfg", srv.URL+"/list.txt", srv.URL+"/modes/{STARTUP}.cfg")
	gi, _ := s.Get("SLUS_900.02")
	if gi.Genre != "Custom" || gi.Players != 4 || gi.ModesMask != 8 || !gi.CompatKnown {
		t.Fatalf("%+v %v", gi, paths)
	}
	// sources off: nothing fetched, nothing known
	s.SetSources("", "", "")
	n := len(paths)
	gi, _ = s.Get("SLUS_900.02")
	if gi.Genre != "" || gi.CompatSrc || len(paths) != n {
		t.Fatalf("%+v", gi)
	}
}
