package formats

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fake struct {
	name  string
	exts  []string
	magic string
}

func (f fake) Name() string         { return f.name }
func (f fake) Extensions() []string { return f.exts }
func (f fake) Probe(context.Context, string) (*Info, error) {
	return &Info{Startup: "SLUS_000.01"}, nil
}
func (f fake) Prepare(_ context.Context, p string, _ *Info, _ Env) (*Prepared, error) {
	return &Prepared{Path: p}, nil
}

type sniffing struct{ fake }

func (s sniffing) Sniff(r io.ReaderAt, size int64) bool {
	b := make([]byte, len(s.magic))
	_, err := r.ReadAt(b, 0)
	return err == nil && string(b) == s.magic
}

func TestRegistry(t *testing.T) {
	Register(fake{name: "AAA", exts: []string{".aaa"}})
	Register(sniffing{fake{name: "MAGIC", exts: []string{".mag"}, magic: "MAGIC!"}})

	dir := t.TempDir()
	plain := filepath.Join(dir, "x.AAA")
	_ = os.WriteFile(plain, []byte("hello"), 0o644)
	if f, err := For(plain); err != nil || f.Name() != "AAA" {
		t.Errorf("by extension (case-insensitive): %v %v", f, err)
	}
	// content wins over a wrong extension
	disguised := filepath.Join(dir, "y.aaa")
	_ = os.WriteFile(disguised, []byte("MAGIC!rest"), 0o644)
	if f, _ := For(disguised); f == nil || f.Name() != "MAGIC" {
		t.Errorf("sniffing: %v", f)
	}
	if _, err := For(filepath.Join(dir, "z.unknown")); err == nil {
		t.Error("unknown extension accepted")
	}
	if !Supported("a/b/C.MAG") || Supported("a.txt") {
		t.Error("Supported")
	}
	if !strings.Contains(Summary(), "MAGIC (.mag)") {
		t.Error("Summary:", Summary())
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate extension did not panic")
		}
	}()
	Register(fake{name: "BBB", exts: []string{".aaa"}})
}

func TestPreparedClose(t *testing.T) {
	n := 0
	p := &Prepared{Cleanup: func() { n++ }}
	p.Close()
	p.Close()
	var nilP *Prepared
	nilP.Close()
	if n != 1 {
		t.Errorf("cleanup ran %d times", n)
	}
}
