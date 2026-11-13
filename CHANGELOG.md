# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [Semantic Versioning](https://semver.org/).

## [1.1.0] — 2026-11-13

### Added
- Application icon: in the Windows executable (with version information), the macOS bundle, the window and taskbar, the header and About; PNG and `.desktop` file in the Linux archives.
- CHD v3 and v4 read by the built-in codec (besides v5), with a CRC check of every hunk; DVDs stored as "hard disk" CHDs by old tools are recognised by their ISO file system.
- New formats: UDF, CSO/ZSO (CSO v1/v2, deflate and LZ4), MDF/MDS (Alcohol 120%), CCD/IMG/SUB (CloneCD); BIN/CUE rewritten (pregap, multiple files, audio tracks, MODE1/MODE2 2352/2336/2048); BIN and IMG without a descriptor.
- Everything written to the PS2 disk has 2048-byte sectors: automatic conversion from 2352, 2336 and 2448 (with subchannel) and a check before writing.
- The files of one image (CUE+BIN, MDS+MDF, CCD+IMG+SUB) are queued only once.
- Detailed Activity log for image problems: format, reason, sector layouts tried, recognition of archives and non-image files.
- `make docs-images`: regenerates the README screenshots.
- HDD-OSD (Browser 2.0): title (name + ID and region) and 3D icon written into the partition header when installing, after renaming/resyncing and with *Artwork → Write HDD-OSD titles & icons* (operation `H` of the batch command, one process for all games). Icons from luissilva1044894's MIT icon pack.
- Complete OPL artwork: front cover, disc icon, background, screenshots 1 and 2 (default theme), back cover, spine and logo (custom themes), from the OPL Manager backup on archive.org read with HTTP Range requests (cached index, one request per picture). Types and archive configurable.
- Complete OPL info sheet: publisher, video mode, aspect ratio, scan mode, with validated values; missing data taken from GameDB-PS2 (developer, publisher, release date). *Database → Write OPL game info* updates all games without touching the compatibility modes.

### Changed
- New project name: **ps2hdl-manager** (executable, repository `github.com/freshness79/ps2hdl-manager`, Go module, identifier `io.github.freshness79.ps2hdl-manager`). Settings and caches in the `ps2hdl-gui` folder are moved over automatically on first start.
- Copyright © 2026 freshness; source code link in About; optional "Buy me a coffee" button in About (`DONATE_URL`) and `.github/FUNDING.yml`.
- Settings split into tabs (*General*, *After installing*, *Online sources*); every online source can be changed: titles, front covers, archive of the other OPL artwork, info sheets, compatibility database, HDD-OSD icons.
- Tests no longer depend on files in `/tmp`: embedded synthetic PS2 image, reference tests enabled by environment variables.
- Removed the option to use external hdl_dump, pfsshell and chdman: the tools are always the built-in ones. The related menu entries (*File → Use an external hdl_dump…*) and settings are gone; *Help → hdl_dump commands available* became *Help → Built-in tools*. CHD v3 and v4, previously read through an external chdman, are now read by the built-in codec.
- All documentation and code comments are in English.
- Disclaimer (no warranty, limitation of liability, user responsibility, trademarks): `DISCLAIMER.md`, README and About; included in the release archives.

### Fixed
- OPL artwork is now PNG: OPL 1.2 and later ignore the JPGs written by earlier versions, which are replaced.
- Details panel: text lines no longer overlap when a game has a developer and a year.

## [1.0.0] — 2026-11-12

First release.

### Added
- Game library of the PS2 disk with genre, multiplayer, compatibility modes and DMA; search.
- Installing from ISO, CUE/BIN, raw BIN/IMG, CHD, NRG, GI, IML with ID detection, title from the online database and diagnostics of errors 111/112 (one-click ISO repair).
- Title resync, rename, delete, extract to ISO.
- OPL covers, info and compatibility modes (`+OPL/ART`, `+OPL/CFG`) and HDL flags for HD Loader.
- CHD conversion and extraction.
- Disk permission check with per-OS instructions; write operations blocked up front.
- Repair of damaged partition IDs.
- Built-in hdl_dump, pfsshell and CHD codec: no external tools required.
- Purely graphical application: no command-line options, no terminal output; activity and crash logs to file.

### Fixed in third-party components
- hdl-dump: `modify` could damage the HDL header or leave partition IDs the PS2 cannot read.
- go-locale: "cannot determine locale" error printed at start-up with `sudo` on macOS.
