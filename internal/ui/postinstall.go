package ui

import (
	"context"
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// What prefetch loads.
const (
	fetchInfo  = 1 << iota // compatibility modes and game info
	fetchArt               // OPL pictures (the configured types)
	fetchIcons             // HDD-OSD 3D icons
)

// prefetch loads game info, OPL art and HDD-OSD icons for ids in parallel, so
// the per-game steps that follow never wait on the network one by one. Source
// problems are logged once each.
func (a *App) prefetch(ids []string, what int) {
	keys := a.artKeys()
	oplm := a.artArchive()
	var mu sync.Mutex
	var probs problemLog
	var missing []string
	jobs := make(chan string)
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if what&fetchInfo != 0 {
					_, _ = a.info.Get(id)
				}
				if what&fetchArt != 0 && len(keys) > 0 {
					have, pr := gamedb.FetchArt(oplm, a.cfg.CoverURL, id, keys)
					mu.Lock()
					for _, p := range pr {
						probs.add(p)
					}
					if len(have) == 0 {
						missing = append(missing, id)
					}
					mu.Unlock()
				}
				if what&fetchIcons != 0 && a.cfg.OSDEnabled() {
					var local problemLog
					a.osdIcon(id, &local)
					mu.Lock()
					for _, m := range local.order {
						probs.add(m)
					}
					mu.Unlock()
				}
			}
		}()
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if n := disc.NormalizeSerial(id); n != "" && !seen[n] {
			seen[n] = true
			jobs <- id
		}
	}
	close(jobs)
	wg.Wait()
	probs.flush(a.log)
	if len(missing) > 0 {
		sort.Strings(missing)
		a.log("No pictures found for " + strings.Join(missing, ", "))
	}
}

// postInstall writes OPL game info/compatibility CFGs and art for freshly
// installed games using exactly two pfsshell sessions: one reading the existing
// CFGs (to keep the user's settings), one writing CFGs and pictures together.
// Compatibility flags for HD Loader are already set by inject. coverN counts
// the games that got at least one picture.
func (a *App) postInstall(games []tools.Game, writeCFG, writeCovers bool) (cfgN, coverN int, err error) {
	if len(games) == 0 || (!writeCFG && !writeCovers) {
		return 0, 0, nil
	}
	ctx := context.Background()
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.Startup)
	}
	what := 0
	if writeCFG {
		what |= fetchInfo
	}
	if writeCovers {
		what |= fetchArt
	}
	a.prefetch(ids, what) // normally already cached

	tmp, err := os.MkdirTemp("", "ps2hdl-post")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(tmp)

	var cmds []string
	if writeCFG {
		var names []string
		for _, g := range games {
			if n := disc.NormalizeSerial(g.Startup); n != "" {
				names = append(names, disc.StartupFromSerial(n)+".cfg")
			}
		}
		existing, err := a.pfs.GetCFGs(ctx, a.cfg.Device, a.cfg.OPLPart, names) // session 1
		if err != nil {
			a.log("Reading existing CFGs: " + err.Error())
		}
		var files []string
		for _, g := range games {
			n := disc.NormalizeSerial(g.Startup)
			if n == "" {
				continue
			}
			name := disc.StartupFromSerial(n) + ".cfg"
			gi, _ := a.info.Get(g.Startup)
			old := gamedb.ParseCFG(existing[name])
			nw := gamedb.MergeGameCFG(old, g.Name, gi, true)
			if gamedb.FormatCFG(nw) == gamedb.FormatCFG(old) {
				continue
			}
			if err := os.WriteFile(filepath.Join(tmp, name), []byte(gamedb.FormatCFG(nw)), 0o644); err != nil {
				return 0, 0, err
			}
			files = append(files, name)
		}
		if len(files) > 0 {
			cmds = append(cmds, tools.RmPutCmds(tools.CfgDir(a.cfg.OPLPart), files)...)
			cfgN = len(files)
		}
	}
	if writeCovers {
		files, rm, n, err := a.stageArt(games, tmp)
		if err != nil {
			return cfgN, 0, err
		}
		if len(files) > 0 {
			cmds = append(cmds, artCmds(a.cfg.OPLPart, files, rm)...)
			coverN = n
		}
	}
	if len(cmds) == 0 {
		return 0, 0, nil
	}
	if _, err := a.pfs.OPLSession(ctx, a.cfg.Device, a.cfg.OPLPart, tmp, cmds, true, a.log); err != nil { // session 2
		return 0, 0, err
	}
	a.log(fmt.Sprintf("OPL: %d CFG and pictures for %d games written in one session", cfgN, coverN))
	return cfgN, coverN, nil
}

// stageArt copies the cached pictures (configured types) of games into dir.
// rm lists legacy names to delete (JPGs that OPL ≥ 1.2 ignores); n is the
// number of games with at least one picture.
func (a *App) stageArt(games []tools.Game, dir string) (files, rm []string, n int, err error) {
	keys := a.artKeys()
	seen := map[string]bool{}
	for _, g := range games {
		id := disc.NormalizeSerial(g.Startup)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		got := false
		for _, k := range keys {
			p := gamedb.CachedArt(g.Startup, k)
			if p == "" {
				continue
			}
			if err := copyFile(p, filepath.Join(dir, filepath.Base(p))); err != nil {
				return nil, nil, 0, err
			}
			files = append(files, filepath.Base(p))
			rm = append(rm, gamedb.LegacyArtNames(g.Startup, k)...)
			got = true
		}
		if got {
			n++
		}
	}
	return files, rm, n, nil
}

// artCmds are the pfsshell commands that put files into ART, deleting rm first.
func artCmds(part string, files, rm []string) []string {
	cmds := tools.RmPutCmds(tools.ArtDir(part), files)
	for _, f := range rm {
		cmds = append(cmds, "rm "+tools.Quote(f))
	}
	return cmds
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
