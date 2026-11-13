package gamedb

import (
	"os"
	"testing"
)

func TestDB(t *testing.T) {
	db := NewTitleDB()
	if err := db.parse([]byte(`{"SLUS-90001":"Sample Game Alpha","SLES-90005":{"title":"Sample Game Gamma: Long Subtitle"}}`)); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.Lookup("SLUS_900.01"); v != "Sample Game Alpha" {
		t.Error(v)
	}
	if v, _ := db.Lookup("SLES_900.05"); CleanTitle(v) != "Sample Game Gamma - Long Subtitle" {
		t.Error(CleanTitle(v))
	}
	if p := os.Getenv("TITLES_JSON"); p != "" {
		b, _ := os.ReadFile(p)
		if err := db.parse(b); err != nil {
			t.Fatal(err)
		}
		v, ok := db.Lookup("SLUS_900.01")
		t.Logf("live DB: %d entries, SLUS_900.01 = %q (%v)", db.Count(), v, ok)
	}
}

func TestCleanFileTitle(t *testing.T) {
	if got := CleanFileTitle("/x/Sample Game Delta (Europe) (En,Fr,De,Es,It) [!].chd"); got != "Sample Game Delta" {
		t.Error(got)
	}
	if got := CleanFileTitle("/x/SLUS_900.01.Sample Game Alpha.iso"); got != "Sample Game Alpha" {
		t.Error(got)
	}
}
