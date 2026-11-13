# PS2 HDL Manager — developer guide

Architecture, building, tests and releases. For using the program see the [README](../README.md).

- [Architecture](#architecture)
- [Built-in tools: hdl_dump and pfsshell](#built-in-tools-hdl_dump-and-pfsshell)
- [Disk access](#disk-access)
- [Build variables](#build-variables)
- [Icon](#icon)
- [Adding an image format](#adding-an-image-format)
- [Building](#building)
- [Tests](#tests)
- [Releasing](#releasing)
- [Third-party code and patches](#third-party-code-and-patches)
- [Conventions](#conventions)

## Architecture

Go module `github.com/freshness79/ps2hdl-manager` (Go ≥ 1.24, cgo). A single executable, `cmd/ps2hdl-manager`; everything else lives in `internal/`.

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/ps2hdl-manager` | `main`: runs a built-in tool (when started as a child process) or the GUI | `tools`, `ui` |
| `internal/ui` | Fyne interface: main window, installer, dialogs, theme | all of the following |
| `internal/tools` | wrappers of hdl_dump (`HDL`), pfsshell (`PFS`), the batch command, `CHDTool`, child process start-up | `apa`, `chd`, `hdldump`, `pfsshell`, `system` |
| `internal/formats` | registry of the format plug-ins (`Format` interface) | — |
| `internal/formats/builtin` | plug-ins ISO, UDF, BIN/CUE, BIN, IMG, CSO/ZSO, NRG, MDF/MDS, CCD/IMG/SUB, CHD, GI/IML, PKG | `formats`, `disc`, `tools` |
| `internal/disc` | image analysis (ISO9660, UDF, SYSTEM.CNF, sector layouts, errors 111/112), CUE/MDS/CCD parsers, CSO/ZSO reader, conversion to 2048, repairs | — |
| `internal/gamedb` | title database, OPL info/compatibility (CFG), OPL artwork (ps2-covers, OPL Manager backup through `RemoteZip`), HDD-OSD data (`icon.sys`, 3D icons) | `disc`, `config` |
| `internal/system` | system disks, permission checks, console detachment | `apa` |
| `internal/config` | settings (`config.json`) and data folder | — |
| `internal/apa` | read-only reader of the APA table and HDL headers | — |
| `internal/chd` | CHD codec in Go: reads v3, v4 (`v34.go`) and v5, writes DVD v5 | — |
| `internal/hdldump`, `internal/pfsshell` | C sources from `third_party/` compiled through cgo | — |
| `internal/buildinfo` | version, source and donation URLs, set with `-ldflags -X` | — |
| `internal/testenv` | test helpers (tools under test, synthetic images); `testenv/sample` holds the embedded sample PS2 image | `tools` |
| root (`legal.go`, package `ps2hdl`) | embedded licence texts and icon (`LICENSE`, `licenses/THIRD_PARTY_NOTICES.txt`, `assets/icon`) | — |

`ui` is the only package that knows about Fyne. The logic (`tools`, `disc`, `gamedb`, `formats`…) can be tested without an interface.

### Main flows

- **Game list**: `apa.Scan` reads the partition table and the HDL headers directly, without starting any process; the result matches `hdl_dump hdl_toc/toc` (checked by `TestScanMatchesHdlDump`). On error it falls back to `hdl_dump hdl_toc`.
- **Installing**: `formats.For` → `Probe` (in the queue) → `Prepare` (a file hdl_dump can read) → `hdl_dump inject_cd/dvd` started in a folder holding `icon.sys` and `list.ico` (`gamedb.HeaderDir`, `HDL.InjectWithHeader`: hdl_dump reads them from its working directory) → one pfsshell session for OPL artwork and CFG files. Info, artwork and icons are downloaded in parallel before starting (`ui.prefetch`).
- **Renames, compatibility flags, ID repair, HDD-OSD**: batch command (`internal/hdldump/batch.c`, operations `R`, `F`, `Z`, `H`): many operations in one process and a single read of the partition table; games are identified by the start sector of their partition. `H <slice> <sector> <folder>` changes into the folder and calls `hdd_inject_header`: header parts without a file in the folder are left as they are.
- **Deleting**: partition, artwork and CFG in a single pfsshell session, then a check by reading the disk again.

### Online data

| Data | Source (default; all changeable in Settings) | Cache |
|---|---|---|
| titles, genres, developer, publisher, release date | GameDB-PS2 (`PS2.data.json`) | `titles-cache.json` |
| OPL info sheet | Tom-Bruise CFG_en | `info/<ID>.cfg` |
| compatibility modes | GDX-X | `info/compat-*` |
| front cover | xlenore/ps2-covers (JPG → PNG 140×200) | `art/` |
| other OPL artwork | OPL Manager backup on archive.org (5.5 GB zip64, stored PNGs) | `art/`, index in `art-index/` |
| HDD-OSD icons | Icon pack for HDL Batch installer (`Icons.ini` + `ICNS/`) | `hddosd/` |

`gamedb.RemoteZip` reads the archive without downloading it: the file tail and the central directory with Range requests (kept in `art-index/`), then one request per picture, CRC checked. Servers without Range support are refused. Pictures that are already PNGs of the right size are copied byte for byte; the others are resized (`gamedb.ArtTypes`). A failing source is logged once and not retried for 5 minutes. Files from non-default info sources are cached under names prefixed with a hash of the source URL.

## Built-in tools: hdl_dump and pfsshell

hdl_dump 0.9.2 and pfsshell are compiled into the executable (`internal/hdldump`, `internal/pfsshell`: `w_*.c` files that include the sources in `third_party/`, built with `-Dmain=…`). Since the C code uses global variables and `exit()`, it always runs in a **separate process**: the app starts itself with the internal environment variable `PS2HDL_INTERNAL_TOOL` (`hdl_dump`, `hdl_batch`, `pfsshell`) set on the child process only (`tools.toolCommand`); `tools.RunEmbeddedTool`, called first in `main`, runs the tool and exits. The app has no command-line options.

There is no way to use external executables: hdl_dump, pfsshell and the CHD codec are always the built-in ones, at the versions in `third_party/` and `internal/chd`. CHD v3/v4 files are read by `internal/chd/v34.go` (fixed-size map entries, zlib, "mini" and self-referencing hunks, CRC32 per hunk); the tests build synthetic v3/v4 files and, with `CHD_REF` pointing to a libchdr-based reader, compare them with that independent implementation.

`PS2HDL_TRACE=1` logs every process started to the activity log (useful to check how many calls an operation makes).

## Disk access

- `system.CheckAccess` opens the device for reading and for writing (without writing) and produces the state shown by the indicator and the per-OS hints.
- `ui.beginWrite` comes before every operation that changes the disk.
- `system.DetachConsole` (Unix) redirects stdin to `/dev/null` and stdout/stderr to the log, and ignores `SIGHUP`; on Windows the executable is a GUI one (`-H windowsgui`).
- On Windows `cmd/ps2hdl-manager/rsrc_windows_amd64.syso` embeds the icon, the `app.manifest` manifest (`requireAdministrator`) and version information: `make windows` regenerates it from `winres.rc` with `x86_64-w64-mingw32-windres` (without it, the committed file is used).

## Build variables

| Variable | Default | Use |
|---|---|---|
| `VERSION` | `git describe`, else 1.1.0 | version shown in About and in the Windows resources |
| `SOURCE_URL` | `https://github.com/freshness79/ps2hdl-manager` | source code link in About (GPL) |
| `DONATE_URL` | empty | "buy me a coffee" page: when set, the button appears in About (`.github/FUNDING.yml` drives GitHub's *Sponsor* button) |

## Icon

Single source: `assets/icon/icon.svg` (original artwork of the project). `make icons` (needs `rsvg-convert`) renders the PNGs from 16 to 1024 px, `icon.ico` (Windows) and `icon.icns` (macOS); the generated files are committed, so normal builds need no graphics tools. Used for: the Windows executable resources, `AppIcon.icns` in the macOS bundle (`make macapp`), the window and taskbar icon at run time (`ps2hdl.AppIcon`, embedded), the header and About, and the PNG + `.desktop` file in the Linux archives of `make dist`.

## Adding an image format

Each format is a plug-in in `internal/formats/builtin` that registers itself; the installer, the file picker and drag & drop need no changes. Example, `internal/formats/builtin/mdf.go`:

```go
package builtin

func init() { formats.Register(mdfFormat{}) }

type mdfFormat struct{}

func (mdfFormat) Name() string         { return "MDF (Alcohol)" }
func (mdfFormat) Extensions() []string { return []string{".mdf"} }

// In the queue: quick analysis. Startup ID, CD/DVD, notes, blocking problems,
// optional Fix, diagnostic text.
func (mdfFormat) Probe(ctx context.Context, path string) (*formats.Info, error) { … }

// When installing: returns a file hdl_dump can read (ISO, CUE…), converting
// into env.WorkDir when needed; Cleanup removes the temporary files.
func (mdfFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) { … }
```

Optional interfaces:
- `Sniff(r io.ReaderAt, size int64) bool`: recognition by content, which takes precedence over the extension;
- `NativeToHDLDump() bool`: hdl_dump reads the format itself and can provide the ID (`cdvd_info`).

**The 2048-byte rule.** `Prepare` must return an ISO with 2048-byte sectors (or the original file if it already is one): the installer checks it with `disc.Verify2048` before writing to the disk and refuses anything else. The only exceptions are formats that declare `NativeToHDLDump` (GI, IML), which hdl_dump converts itself.

Most plug-ins boil down to "find the data track": `builtin/track.go` does the rest. A plug-in builds a `trackState` with a function that opens the track (`fileTrack(file, start, end)` or any `io.ReaderAt`, such as the CSO reader) and calls `probeTrack` / `prepareTrack`, which detect the sector layout, read the ID, fill in notes and details, convert to 2048 in the work folder and repair the path table on the copy.

Ready-made helpers in `internal/disc`:
- `DetectLayout` (2048, 2352 MODE1/MODE2, 2336, 2448 with subchannel; UDF), with `LayoutError` listing what was looked for and `MagicHint` recognising archives and other formats;
- `WriteISO` (any track → 2048 ISO), `ExtractTrackISO`, `Verify2048`;
- `Inspect` / `InspectReader` (ID, errors 111/112, UDF-only), `RepairPathTable`, `RepairPathTableInPlace`;
- `ParseCue`, `ParseMDS`, `ParseCCD` (→ `TrackRef`), `OpenCISO` (CSO/ZSO as an `io.ReaderAt`).

Formats made of several files: implement `Primary(path) string` (`formats.Grouper` interface) so that every file of the group leads to the same descriptor and the image is queued only once.

Errors must explain the problem. The `Probe`/`Prepare` message becomes the main line in the Activity log, and `Info.Details` (or the detail of a `disc.LayoutError`) the lines below it.

For tests: `internal/formats/builtin/builtin_test.go` checks every plug-in with synthetic images (`testenv.ToLayout`, `testenv.MDS`, `testenv.CCD`, `testenv.NRGv2DAO`, `testenv.CHDv4CD`…), with images made by independent tools (`genisoimage` for ISO/UDF, `scripts/make_test_images.py` with zlib and python-lz4 for CSO/ZSO; skipped when missing) and with a real install on a virtual disk followed by reading the game back from the PS2 disk, which must match the source ISO byte for byte.

## Building

Prerequisites: Go ≥ 1.24, a C compiler, `make`.

| System | Packages |
|---|---|
| macOS | Xcode Command Line Tools (`xcode-select --install`) |
| Linux | `gcc`, `libgl1-mesa-dev`, `xorg-dev` |
| Windows (cross) | `mingw-w64` (`x86_64-w64-mingw32-gcc`) |

| Command | Result |
|---|---|
| `make` / `make build` | `bin/ps2hdl-manager` for this system |
| `make mac` / `make macapp` | macOS binary / unsigned `.app` bundle (on a Mac) |
| `make windows` | `bin/ps2hdl-manager.exe` |
| `make linux` | `bin/ps2hdl-manager-linux-<arch>` |

See [Build variables](#build-variables). Example: `make windows VERSION=1.1.0 DONATE_URL=https://ko-fi.com/<name>`.

Without make: `CGO_ENABLED=1 go build -ldflags "-X github.com/freshness79/ps2hdl-manager/internal/buildinfo.Version=1.1.0" ./cmd/ps2hdl-manager` (on Windows add `-H windowsgui`).

## Tests

| Command | What it checks | Needs |
|---|---|---|
| `make test` | unit tests of all packages | — |
| `make check` | `go vet` + tests + formatting | — |
| `make race` | tests with the race detector | — |
| `make test-integration REAL_DEV=/dev/loopN` | install, rename, batch, ID repair, HDD-OSD header (read back byte by byte from the disk), OPL artwork and CFG, plug-ins, APA scanner with the real tools | a disk that may be **overwritten** |
| `make test-access` | permission check as an unprivileged user | Linux, root (the test runs as `nobody`) |
| `make shot` | software-rendered UI screenshots in `bin/shots/` | — |
| `make docs-images` | regenerates the README screenshots in `docs/images` (clean sample data) | — |

Virtual disk for the integration tests (Linux):

```bash
truncate -s 20G /tmp/ps2disk.img
DEV=$(sudo losetup -f --show /tmp/ps2disk.img)
make build
printf 'device %s\ninitialize yes\nexit\n' $DEV | PS2HDL_INTERNAL_TOOL=pfsshell bin/ps2hdl-manager
make test-integration REAL_DEV=$DEV
```

The tests use an embedded synthetic PS2 image (`internal/testenv/sample`: 26 sectors, only `SYSTEM.CNF` and a stand-in file for the ELF, no game data) and broken variants of it (errors 111 and 112); temporary files go to test folders that Go removes by itself.

The format tests with reference images need `genisoimage` and the Python `lz4` module (`apt install genisoimage`, `pip install lz4`); without them they are skipped.

Optional reference tests, skipped when the variable is not set:

| Variable | Contents |
|---|---|
| `CHD_TESTDATA` | folder with `mix.iso` and one CHD per codec made with MAME's `chdman` (compares the CHD reader with the original program) |
| `CHD_REF` + `CHD_V34_DUMP` | a libchdr-based reader and a work folder: compares the synthetic CHD v3/v4 files with an independent implementation |
| `CHD_BENCH` | a large CHD to time the extraction |

## Releasing

1. Update `CHANGELOG.md` and create the tag (`git tag v1.1.0`): `VERSION` is taken from the tag (without a tag it is the commit hash). If the interface changed, run `make docs-images`.
2. If `go.mod` changed: build for every platform and regenerate the licence notices with `make notices` (it uses the binaries found in `bin/`), then build again to embed them.
3. `make windows`, `make macapp` (on a Mac), `make` (Linux), then `make dist`.
4. Publish the source archive `dist/ps2hdl-manager-<version>-src.tar.gz` with the binaries (or point `SOURCE_URL` to the repository): the GPL requires it.

## Third-party code and patches

| Component | Folder | Licence | Local patches |
|---|---|---|---|
| hdl-dump 0.9.2 | `third_party/hdl-dump` | GPL-2.0-or-later | [PATCHES.md](../third_party/PATCHES.md): unterminated buffers and partition IDs not zeroed in `hdl_modify_game` |
| pfsshell (+ ps2sdk) | `third_party/pfsshell` | GPL-2.0 / AFL-2.0 | — |
| go-locale | `third_party/go-locale` | MIT | [PATCHES-PS2HDL.md](../third_party/go-locale/PATCHES-PS2HDL.md): reading the language without errors under `sudo` on macOS (linked with `replace` in `go.mod`) |

The CHD algorithms follow MAME (BSD-3-Clause); the ECC tables are generated from MAME's `cdrom.cpp`.

`licenses/THIRD_PARTY_NOTICES.txt` is **generated** by `scripts/gen_notices.py` from the modules actually compiled into the executables (do not edit it by hand).

## Conventions

- Code, comments, interface texts and documentation are in English.
- Code formatted with `gofmt` (`make fmt`; `make check` verifies it).
- C code in `third_party/` stays as close as possible to the original: every change is documented in the related patch file.
- Every new operation that writes to the disk must go through `beginWrite` (permission check) and, where possible, use the batch command or a single pfsshell session.
