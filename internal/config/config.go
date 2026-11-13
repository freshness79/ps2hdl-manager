package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds the persistent settings of the application.
type Config struct {
	Device     string `json:"device"`      // last selected disk (e.g. hdd1:)
	DBURL      string `json:"db_url"`      // URL of the online title database
	DMAMode    string `json:"dma_mode"`    // default DMA mode (e.g. *u4)
	LastFolder string `json:"last_folder"` // last folder used for images
	OPLPart    string `json:"opl_part"`    // OPL data partition (default +OPL)
	CoverURL   string `json:"cover_url"`   // cover URL template
	AutoCovers *bool  `json:"auto_covers"` // install covers after installing games
	AutoCompat *bool  `json:"auto_compat"` // apply recommended modes + game info after installing
	WorkDir    string `json:"work_dir"`    // where CHDs are decompressed (needs space)

	// AutoOSD: write the HDD-OSD (Browser 2.0) title and 3D icon at install time.
	AutoOSD *bool `json:"auto_osd"`
	// ArtKinds: OPL art types to install (COV, ICO, BG…); nil = the default theme's.
	ArtKinds []string `json:"art_types"`
	// ArtArchive: OPL Manager art backup (zip URL); "-" disables it.
	ArtArchive string `json:"art_archive"`
	// IconIndex: HDD-OSD icon pack index (Icons.ini URL); "-" disables icons.
	IconIndex string `json:"icon_index"`
	// InfoURL: OPL info sheet per game ({STARTUP} = SLUS_900.02); "-" disables it.
	InfoURL string `json:"info_url"`
	// CompatListURL / CompatCfgURL: compatibility mode database; "-" disables it.
	CompatListURL string `json:"compat_list_url"`
	CompatCfgURL  string `json:"compat_cfg_url"`
}

func (c *Config) CompatEnabled() bool { return c.AutoCompat == nil || *c.AutoCompat }

func (c *Config) CoversEnabled() bool { return c.AutoCovers == nil || *c.AutoCovers }

func (c *Config) OSDEnabled() bool { return c.AutoOSD == nil || *c.AutoOSD }

// ArtArchiveURL is the art backup to use, "" when disabled.
func (c *Config) ArtArchiveURL() string {
	switch c.ArtArchive {
	case "":
		return DefaultArtArchive
	case "-":
		return ""
	}
	return c.ArtArchive
}

// IconIndexURL is the HDD-OSD icon index to use, "" when disabled.
func (c *Config) IconIndexURL() string {
	switch c.IconIndex {
	case "":
		return DefaultIconIndex
	case "-":
		return ""
	}
	return c.IconIndex
}

// source returns the URL to use for a setting ("" = default, "-" = off → "").
func source(v, def string) string {
	switch v {
	case "":
		return def
	case "-":
		return ""
	}
	return v
}

func (c *Config) InfoSource() string       { return source(c.InfoURL, DefaultInfoURL) }
func (c *Config) CompatListSource() string { return source(c.CompatListURL, DefaultCompatListURL) }
func (c *Config) CompatCfgSource() string  { return source(c.CompatCfgURL, DefaultCompatCfgURL) }

// Default game info sources: Tom-Bruise's OPL info sheets and GDX-X's
// compatibility database ({STARTUP} = SLUS_900.02).
const (
	DefaultInfoURL       = "https://raw.githubusercontent.com/Tom-Bruise/PS2-OPL-CFG-Database/master/CFG_en/{STARTUP}.cfg"
	DefaultCompatListURL = "https://raw.githubusercontent.com/GDX-X/PS2-OPL-CFG-Compatibility-Database/main/Games_List.txt"
	DefaultCompatCfgURL  = "https://raw.githubusercontent.com/GDX-X/PS2-OPL-CFG-Compatibility-Database/main/HDD/{STARTUP}.cfg"
)

// DefaultArtArchive is OPL Manager's last complete art backup (archive.org).
const DefaultArtArchive = "https://archive.org/download/OPLM_ART_2024_09/OPLM_ART_2024_09.zip"

// DefaultIconIndex is the HDD-OSD icon pack index (luissilva1044894, MIT);
// icon files live in ICNS/ next to it.
const DefaultIconIndex = "https://raw.githubusercontent.com/luissilva1044894/Icon-pack-for-HDL-Batch-installer/master/Icons.ini"

const DefaultDBURL = "https://github.com/niemasd/GameDB-PS2/releases/latest/download/PS2.data.json"

const oldDBURL = "https://github.com/niemasd/GameDB-PS2/releases/latest/download/PS2.titles.json"

func Dir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	nd := filepath.Join(d, "ps2hdl-manager")
	if _, err := os.Stat(nd); os.IsNotExist(err) {
		// settings and caches of versions before the rename move over once
		_ = os.Rename(filepath.Join(d, "ps2hdl-gui"), nd)
	}
	_ = os.MkdirAll(nd, 0o755)
	return nd
}

func Path() string { return filepath.Join(Dir(), "config.json") }

func Load() *Config {
	c := &Config{DBURL: DefaultDBURL, DMAMode: "*u4"}
	b, err := os.ReadFile(Path())
	if err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.DBURL == "" || c.DBURL == oldDBURL { // titles-only DB → full DB (adds genres)
		c.DBURL = DefaultDBURL
	}
	if c.DMAMode == "" {
		c.DMAMode = "*u4"
	}
	if c.OPLPart == "" {
		c.OPLPart = "+OPL"
	}
	if c.CoverURL == "" {
		c.CoverURL = DefaultCoverURL
	}
	return c
}

func (c *Config) Save() error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// write to a temp file and rename: a crash never leaves a truncated config
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

const DefaultCoverURL = "https://raw.githubusercontent.com/xlenore/ps2-covers/main/covers/default/{ID}.jpg"
