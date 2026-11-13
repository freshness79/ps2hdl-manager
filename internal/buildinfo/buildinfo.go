// Package buildinfo holds values set at build time by the Makefile:
//
//	-ldflags "-X github.com/freshness79/ps2hdl-manager/internal/buildinfo.Version=1.2.3 -X github.com/freshness79/ps2hdl-manager/internal/buildinfo.SourceURL=https://…"
package buildinfo

// Version of the application.
var Version = "1.1.0"

// SourceURL is where the corresponding source code is published (GPL); empty
// when the source code is distributed together with the program.
var SourceURL = "https://github.com/freshness79/ps2hdl-manager"

// DonateURL is an optional "buy me a coffee" page shown in About; empty hides it.
var DonateURL = ""

// Author holds the copyright of the program.
const Author = "freshness"
