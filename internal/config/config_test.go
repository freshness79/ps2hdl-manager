package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirMigratesOldName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	base, _ := os.UserConfigDir()
	old := filepath.Join(base, "ps2hdl-gui")
	_ = os.MkdirAll(old, 0o755)
	_ = os.WriteFile(filepath.Join(old, "config.json"), []byte(`{"opl_part":"+MINE","hdl_path":"/x","external_tools":true}`), 0o644)
	if d := Dir(); d != filepath.Join(base, "ps2hdl-manager") {
		t.Fatal(d)
	}
	if c := Load(); c.OPLPart != "+MINE" {
		t.Fatal("settings not carried over:", c.OPLPart)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old folder still there")
	}
}

func TestSources(t *testing.T) {
	c := &Config{}
	if c.InfoSource() != DefaultInfoURL || c.CompatCfgSource() != DefaultCompatCfgURL {
		t.Fatal("defaults")
	}
	c.InfoURL, c.CompatListURL = "-", "https://example.org/list.txt"
	if c.InfoSource() != "" || c.CompatListSource() != "https://example.org/list.txt" {
		t.Fatal("custom/off")
	}
}
