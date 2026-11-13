package gamedb

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freshness79/ps2hdl-manager/internal/config"
)

func pngOf(w, h int, c color.Color) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// artArchive builds an OPL Manager-like backup: more than 65535 members (so the
// writer must use zip64 records), stored like the real one, plus a PS1 picture
// with a PS2 ID that must be ignored.
func artArchive(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	}
	add("PS1/SLUS_900.02/SLUS_900.02_COV.png", pngOf(140, 200, color.White)) // must not win
	add("PS2/SLUS_900.02/SLUS_900.02_COV.png", pngOf(140, 200, color.NRGBA{200, 0, 0, 255}))
	add("PS2/SLUS_900.02/SLUS_900.02_ICO.png", pngOf(64, 64, color.NRGBA{0, 200, 0, 255}))
	add("PS2/SLUS_900.02/SLUS_900.02_BG_00.png", pngOf(320, 240, color.NRGBA{0, 0, 200, 255})) // wrong size: resized
	add("PS2/SLUS_900.02/SLUS_900.02_SCR_01.png", pngOf(250, 188, color.Black))
	add("PS2/SLUS_900.02/SLUS_900.02_LGO.png", pngOf(600, 100, color.NRGBA{9, 9, 9, 255}))
	for i := 0; i < 66000; i++ {
		add("PS2/FILL_000.00/x"+itoa(i), nil)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if !bytes.Contains(b[len(b)-200:], []byte("PK\x06\x06")) {
		t.Fatal("test archive has no zip64 records")
	}
	return b
}

func itoa(i int) string {
	return strings.TrimLeft(string([]byte{byte('0' + i/10000%10), byte('0' + i/1000%10), byte('0' + i/100%10), byte('0' + i/10%10), byte('0' + i%10)}), "")
}

// archiveServer mimics archive.org: /download/... redirects to the data server,
// which honours Range. It counts data requests.
func archiveServer(t *testing.T, data []byte, honourRange bool) (*httptest.Server, *int32) {
	var n int32
	mux := http.NewServeMux()
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/data/backup.zip", http.StatusFound)
	})
	mux.HandleFunc("/data/backup.zip", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		if !honourRange {
			w.WriteHeader(200)
			_, _ = w.Write(data[:1000])
			return
		}
		http.ServeContent(w, r, "backup.zip", time.Time{}, bytes.NewReader(data))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestOPLMArtRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	data := artArchive(t)
	srv, reqs := archiveServer(t, data, true)
	url := srv.URL + "/download/OPLM_TEST/OPLM_TEST.zip"

	o := NewOPLMArt(url)
	have, err := o.Available("SLUS_900.02")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(have, ",") != "COV,ICO,BG,SCR2,LGO" {
		t.Errorf("available %v", have)
	}
	indexReqs := atomic.LoadInt32(reqs)
	if indexReqs > 3 {
		t.Errorf("reading the index took %d requests", indexReqs)
	}
	for _, k := range []string{"COV", "ICO", "BG", "SCR2", "LGO"} {
		at, _ := ArtTypeByKey(k)
		before := atomic.LoadInt32(reqs)
		p, err := o.Fetch("SLUS_900.02", at)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if d := atomic.LoadInt32(reqs) - before; d > 1 {
			t.Errorf("%s: %d requests for one picture", k, d)
		}
		f, _ := os.Open(p)
		cfg, format, err := image.DecodeConfig(f)
		f.Close()
		if err != nil || format != "png" || cfg.Width != at.W || cfg.Height != at.H {
			t.Errorf("%s: %s %dx%d (want png %dx%d) %v", k, format, cfg.Width, cfg.Height, at.W, at.H, err)
		}
		if !strings.HasSuffix(p, "SLUS_900.02_"+k+".png") {
			t.Errorf("%s cached as %s", k, p)
		}
	}
	// the PS2 cover, not the PS1 one, and kept byte for byte (already 140x200 PNG)
	got, _ := os.ReadFile(CachedArt("SLUS_900.02", "COV"))
	if !bytes.Equal(got, pngOf(140, 200, color.NRGBA{200, 0, 0, 255})) {
		t.Error("cover: not the PS2 picture kept as is")
	}
	at, _ := ArtTypeByKey("COV2")
	if _, err := o.Fetch("SLUS_900.02", at); err != ErrNoArt {
		t.Errorf("missing type: %v", err)
	}

	// second start: index from the disk cache, no request
	before := atomic.LoadInt32(reqs)
	o2 := NewOPLMArt(url)
	if _, err := o2.Available("SLUS_900.02"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(reqs) != before {
		t.Error("index read again instead of using the cache")
	}
	if _, err := os.Stat(filepath.Join(config.Dir(), "art-index", "OPLM_TEST.zip.idx")); err != nil {
		t.Error("index cache not written:", err)
	}
}

func TestRemoteZipRefusesFullDownload(t *testing.T) {
	srv, _ := archiveServer(t, artArchive(t), false)
	_, err := OpenRemoteZip(srv.URL+"/download/x/x.zip", "", "test")
	if err == nil || !strings.Contains(err.Error(), "range request not honoured") {
		t.Errorf("expected a refusal, got %v", err)
	}
}

func TestRemoteZipChecksum(t *testing.T) {
	data := artArchive(t)
	i := bytes.Index(data, pngOf(64, 64, color.NRGBA{0, 200, 0, 255}))
	bad := append([]byte(nil), data...)
	bad[i+60] ^= 0xff // corrupt the icon's data, not its directory entry
	srv, _ := archiveServer(t, bad, true)
	z, err := OpenRemoteZip(srv.URL+"/download/y/y.zip", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.Members() {
		if strings.HasSuffix(f.Name, "_ICO.png") {
			if _, err := z.ReadMember(f, 1<<20); err == nil || !strings.Contains(err.Error(), "checksum") {
				t.Errorf("corrupted member accepted: %v", err)
			}
			return
		}
	}
	t.Fatal("icon member not found")
}
