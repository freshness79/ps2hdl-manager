# PS2 HDL Manager — build helpers.  `make help` lists the targets.
#
# Requirements: Go ≥ 1.24 and a C compiler (cgo: Fyne + the built-in hdl_dump/pfsshell).
#   macOS:   xcode-select --install
#   Linux:   gcc, libgl1-mesa-dev, xorg-dev
#   Windows cross-build from Linux/macOS: mingw-w64 (x86_64-w64-mingw32-gcc)

APP        := ps2hdl-manager
APPNAME    := PS2 HDL Manager
BUNDLE_ID  := io.github.freshness79.ps2hdl-manager
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || true)
ifeq ($(strip $(VERSION)),)
VERSION    := 1.1.0
endif
SOURCE_URL ?= https://github.com/freshness79/ps2hdl-manager
# optional donation page shown in About (e.g. https://ko-fi.com/<name>); empty hides the button
DONATE_URL ?=

GO         ?= go
GOOS       := $(shell $(GO) env GOOS)
GOARCH     := $(shell $(GO) env GOARCH)
EXE        := $(if $(filter windows,$(GOOS)),.exe,)

BIN        := bin
DIST       := dist
PKG        := ./cmd/ps2hdl-manager
BI         := github.com/freshness79/ps2hdl-manager/internal/buildinfo
LDFLAGS    := -s -w -X '$(BI).Version=$(VERSION)' -X '$(BI).SourceURL=$(SOURCE_URL)' -X '$(BI).DonateURL=$(DONATE_URL)'
GOBUILD    := CGO_ENABLED=1 $(GO) build -trimpath -ldflags "$(LDFLAGS)"

WIN_CC     ?= x86_64-w64-mingw32-gcc
WIN_RC     ?= x86_64-w64-mingw32-windres
# numeric version for Windows resources: 1.2.3 from "1.2.3-4-gabc" (0.0.0 for a bare hash)
VER_NUM    := $(shell echo '$(VERSION)' | sed -E 's/^([0-9]+)\.([0-9]+)\.?([0-9]*).*/\1 \2 \3/;t;s/.*/0 0 0/')

# integration tests: a PS2/APA disk or loop device that may be WRITTEN TO (never a real library!)
REAL_DEV   ?=

.DEFAULT_GOAL := build
.PHONY: help build run windows winres icons mac macapp linux test test-integration test-access race vet fmt check \
        shot docs-images notices licenses dist src clean

help: ## show this help
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  \033[1m%-17s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "  Variables: VERSION=$(VERSION)  SOURCE_URL=$(SOURCE_URL)  REAL_DEV=$(REAL_DEV)"

# ---------------------------------------------------------------- build

build: ## build for this computer → bin/ps2hdl-manager
	@mkdir -p $(BIN)
	$(GOBUILD) -o $(BIN)/$(APP)$(EXE) $(PKG)
	@echo "→ $(BIN)/$(APP)$(EXE)  ($(VERSION), $(GOOS)/$(GOARCH))"

run: build ## build and start (raw disk access may need sudo on macOS/Linux)
	./$(BIN)/$(APP)$(EXE)

linux: ## Linux binary (on a Linux host) → bin/ps2hdl-manager-linux-<arch>
	@mkdir -p $(BIN)
	GOOS=linux $(GOBUILD) -o $(BIN)/$(APP)-linux-$(GOARCH) $(PKG)

windows: ## Windows x64 .exe (cross-build with mingw-w64) → bin/ps2hdl-manager.exe
	@command -v $(WIN_CC) >/dev/null || { echo "missing $(WIN_CC): install mingw-w64"; exit 1; }
	@mkdir -p $(BIN)
	@$(MAKE) --no-print-directory winres
	GOOS=windows GOARCH=amd64 CC=$(WIN_CC) CGO_ENABLED=1 \
		$(GO) build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(BIN)/$(APP).exe $(PKG)
	@echo "→ $(BIN)/$(APP).exe (asks for Administrator: needed for raw disk access)"

winres: ## Windows resources (icon, manifest, version) → cmd/ps2hdl-manager/rsrc_windows_amd64.syso
	@if command -v $(WIN_RC) >/dev/null; then \
		set -- $(VER_NUM); \
		cd cmd/ps2hdl-manager && $(WIN_RC) -O coff -F pe-x86-64 \
			-DVER_MAJOR=$$1 -DVER_MINOR=$$2 -DVER_PATCH=$${3:-0} -DVER_STR='\"$(VERSION)\"' \
			-i winres.rc -o rsrc_windows_amd64.syso && echo "→ Windows resources $(VERSION)"; \
	else echo "($(WIN_RC) not found: using the committed rsrc_windows_amd64.syso)"; fi

icons: ## re-render the app icon (assets/icon) from icon.svg: needs rsvg-convert
	python3 scripts/make_icons.py

mac: ## macOS binary for this Mac's architecture (run on macOS)
	@test "$(GOOS)" = darwin || { echo "run 'make mac' on macOS"; exit 1; }
	@mkdir -p $(BIN)
	$(GOBUILD) -o $(BIN)/$(APP)-darwin-$(GOARCH) $(PKG)

macapp: mac ## "PS2 HDL Manager.app" bundle (unsigned) in bin/
	@rm -rf "$(BIN)/$(APPNAME).app"
	@mkdir -p "$(BIN)/$(APPNAME).app/Contents/MacOS" "$(BIN)/$(APPNAME).app/Contents/Resources"
	@cp $(BIN)/$(APP)-darwin-$(GOARCH) "$(BIN)/$(APPNAME).app/Contents/MacOS/$(APP)"
	@cp LICENSE "$(BIN)/$(APPNAME).app/Contents/Resources/LICENSE.txt"
	@cp DISCLAIMER.md "$(BIN)/$(APPNAME).app/Contents/Resources/DISCLAIMER.md"
	@cp assets/icon/icon.icns "$(BIN)/$(APPNAME).app/Contents/Resources/AppIcon.icns"
	@cp licenses/THIRD_PARTY_NOTICES.txt "$(BIN)/$(APPNAME).app/Contents/Resources/"
	@printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
	 '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
	 '<plist version="1.0"><dict>' \
	 '<key>CFBundleName</key><string>$(APPNAME)</string>' \
	 '<key>CFBundleDisplayName</key><string>$(APPNAME)</string>' \
	 '<key>CFBundleIdentifier</key><string>$(BUNDLE_ID)</string>' \
	 '<key>CFBundleExecutable</key><string>$(APP)</string>' \
	 '<key>CFBundlePackageType</key><string>APPL</string>' \
	 '<key>CFBundleIconFile</key><string>AppIcon</string>' \
	 '<key>CFBundleShortVersionString</key><string>$(VERSION)</string>' \
	 '<key>CFBundleVersion</key><string>$(VERSION)</string>' \
	 '<key>LSMinimumSystemVersion</key><string>11.0</string>' \
	 '<key>NSHighResolutionCapable</key><true/>' \
	 '</dict></plist>' > "$(BIN)/$(APPNAME).app/Contents/Info.plist"
	@echo "→ $(BIN)/$(APPNAME).app (unsigned; raw disk access: sudo \"$(BIN)/$(APPNAME).app/Contents/MacOS/$(APP)\")"

# ---------------------------------------------------------------- quality

test: ## unit tests (no disk needed)
	$(GO) test -count=1 ./...

test-integration: build ## integration tests on REAL_DEV (ERASES/MODIFIES it! use a loop device or spare disk)
	@test -n "$(REAL_DEV)" || { echo "set REAL_DEV=/dev/loopN (a disk the tests may overwrite)"; exit 1; }
	REAL_DEV=$(REAL_DEV) REAL_SELF=$(CURDIR)/$(BIN)/$(APP)$(EXE) $(GO) test -p 1 -count=1 ./internal/apa ./internal/tools ./internal/formats/builtin ./internal/ui

test-access: ## permission check as an unprivileged user (Linux, run as root: uses runuser/nobody)
	@test "$$(id -u)" = 0 || { echo "run as root (the test itself runs as 'nobody')"; exit 1; }
	$(GO) test -c -o /tmp/ps2hdl-access.test ./internal/system
	@echo x > /tmp/ps2hdl-ro.img && chmod 644 /tmp/ps2hdl-ro.img
	@echo x > /tmp/ps2hdl-none.img && chmod 600 /tmp/ps2hdl-none.img
	cd /tmp && runuser -u nobody -- env HOME=/tmp ACCESS_RO=/tmp/ps2hdl-ro.img ACCESS_NONE=/tmp/ps2hdl-none.img \
		/tmp/ps2hdl-access.test -test.run 'TestAccessNonRoot' -test.v
	@rm -f /tmp/ps2hdl-access.test /tmp/ps2hdl-ro.img /tmp/ps2hdl-none.img

race: ## tests with the race detector (CHD writer test skipped: too memory-hungry under -race)
	$(GO) test -race -count=1 $$($(GO) list ./... | grep -v /internal/chd)
	$(GO) test -race -count=1 -run 'TestReadAllCodecs|TestRawCDWithECC' ./internal/chd

vet: ## go vet
	$(GO) vet ./...

fmt: ## gofmt all Go files
	gofmt -w cmd internal legal.go

check: vet test ## vet + unit tests (+ unformatted-file check)
	@out=$$(gofmt -l cmd internal legal.go); \
	 if [ -n "$$out" ]; then echo "not gofmt-ed:"; echo "$$out"; exit 1; fi

shot: ## software-rendered UI screenshots → bin/shots/*.png (no display needed)
	SHOT_OUT=$(CURDIR)/$(BIN)/shots $(GO) test -tags shot -count=1 -run TestShot ./internal/ui
	@ls -1 $(BIN)/shots

docs-images: ## regenerate the README screenshots in docs/images (clean sample data)
	SHOT_DOCS=$(CURDIR)/docs/images $(GO) test -tags shot -count=1 -run TestShot ./internal/ui
	@ls -1 docs/images

# ---------------------------------------------------------------- licenses & packaging

notices: build ## regenerate licenses/THIRD_PARTY_NOTICES.txt (adds bin/*.exe and darwin binaries if present)
	python3 scripts/gen_notices.py $(BIN)/$(APP)$(EXE) $(wildcard $(BIN)/$(APP).exe $(BIN)/$(APP)-darwin-*)
	@echo "rebuild to embed the new notices: make build"

licenses: ## copy LICENSE.txt + THIRD_PARTY_NOTICES.txt into bin/
	@mkdir -p $(BIN)
	cp LICENSE $(BIN)/LICENSE.txt
	cp licenses/THIRD_PARTY_NOTICES.txt $(BIN)/

dist: ## release archives in dist/: one per binary found in bin/, each with licenses
	@mkdir -p $(DIST)
	@found=0; for b in $(BIN)/$(APP) $(BIN)/$(APP).exe $(BIN)/$(APP)-linux-* $(BIN)/$(APP)-darwin-*; do \
	  [ -f "$$b" ] || continue; found=1; \
	  name=$$(basename "$$b" .exe); case "$$b" in *.exe) plat=windows-amd64;; *-darwin-*|*-linux-*) plat=$${name#$(APP)-};; *) plat=$(GOOS)-$(GOARCH);; esac; \
	  dir=$(DIST)/$(APP)-$(VERSION)-$$plat; rm -rf "$$dir"; mkdir -p "$$dir"; \
	  cp "$$b" "$$dir/"; cp README.md "$$dir/"; \
	  cp LICENSE "$$dir/LICENSE.txt"; cp DISCLAIMER.md "$$dir/"; cp licenses/THIRD_PARTY_NOTICES.txt "$$dir/"; \
	  case "$$plat" in linux-*) cp assets/icon/icon-256.png "$$dir/ps2hdl-manager.png"; \
	    sed 's#^Exec=.*#Exec=ps2hdl-manager#' assets/linux/ps2hdl-manager.desktop > "$$dir/ps2hdl-manager.desktop";; esac; \
	  case "$$b" in *.exe) (cd $(DIST) && rm -f "$$(basename $$dir).zip" && zip -qr "$$(basename $$dir).zip" "$$(basename $$dir)");; \
	                 *) tar -C $(DIST) -czf "$$dir.tar.gz" "$$(basename $$dir)";; esac; \
	  rm -rf "$$dir"; echo "→ $$dir.$$(case $$b in *.exe) echo zip;; *) echo tar.gz;; esac)"; \
	done; [ $$found = 1 ] || { echo "no binaries in $(BIN)/: run make build / windows / mac first"; exit 1; }
	@$(MAKE) --no-print-directory src

src: ## source archive (GPL: ship it with the binaries) → dist/ps2hdl-manager-<ver>-src.tar.gz
	@mkdir -p $(DIST)/.stage
	@ln -sfn "$(CURDIR)" "$(DIST)/.stage/$(APP)-$(VERSION)"
	@# -h follows the link; works with GNU tar (Linux) and bsdtar (macOS)
	tar -C $(DIST)/.stage -czhf $(DIST)/$(APP)-$(VERSION)-src.tar.gz \
	    --exclude='$(APP)-$(VERSION)/$(DIST)' --exclude='$(APP)-$(VERSION)/$(BIN)' --exclude='$(APP)-$(VERSION)/.git' \
	    --exclude='*.exe' --exclude='*.test' --exclude='$(APP)-$(VERSION)/info.sys' \
	    $(APP)-$(VERSION)
	@rm -rf $(DIST)/.stage
	@echo "→ $(DIST)/$(APP)-$(VERSION)-src.tar.gz"

clean: ## remove build outputs and test leftovers
	rm -rf $(BIN) $(DIST)/$(APP)-* $(DIST)/.stage *.test info.sys
