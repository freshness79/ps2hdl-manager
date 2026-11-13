package builtin

// Format plug-in for images hdl_dump reads by itself and that need no work here:
// the file is passed to hdl_dump as is, which also finds the startup ID
// (installer fallback: hdl_dump cdvd_info). The smallest possible plug-in.

import (
	"context"

	"github.com/freshness79/ps2hdl-manager/internal/formats"
)

type nativeFormat struct {
	name string
	exts []string
}

func init() {
	formats.Register(nativeFormat{"GI (Global Image)", []string{".gi"}})
	formats.Register(nativeFormat{"IML", []string{".iml"}})
}

func (f nativeFormat) Name() string         { return f.name }
func (f nativeFormat) Extensions() []string { return f.exts }
func (nativeFormat) NativeToHDLDump() bool  { return true }

func (f nativeFormat) Probe(context.Context, string) (*formats.Info, error) {
	return &formats.Info{Notes: []string{f.name + " · read directly by hdl_dump"}}, nil
}

func (nativeFormat) Prepare(_ context.Context, path string, _ *formats.Info, _ formats.Env) (*formats.Prepared, error) {
	return &formats.Prepared{Path: path}, nil
}
