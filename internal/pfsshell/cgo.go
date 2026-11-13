// Package pfsshell embeds pfsshell (ps2homebrew/pfsshell, GPL-2.0; ps2sdk parts AFL-2.0).
// The original C sources are vendored in third_party/pfsshell.
package pfsshell

/*
#cgo CFLAGS: -Wno-error=implicit-function-declaration -Wno-error=int-conversion -Wno-error=incompatible-pointer-types
#cgo darwin CFLAGS: -Wno-error=incompatible-function-pointer-types
#cgo CFLAGS: -O0 -D_FILE_OFFSET_BITS=64 -w -Dmain=pfsshell_c_main
#cgo CFLAGS: -I${SRCDIR}/../../third_party/pfsshell -I${SRCDIR}/../../third_party/pfsshell/subprojects/apa -I${SRCDIR}/../../third_party/pfsshell/subprojects/apa/include -I${SRCDIR}/../../third_party/pfsshell/subprojects/apa/irx
#cgo CFLAGS: -I${SRCDIR}/../../third_party/pfsshell/subprojects/pfs -I${SRCDIR}/../../third_party/pfsshell/subprojects/pfs/include -I${SRCDIR}/../../third_party/pfsshell/subprojects/pfs/irx
#cgo CFLAGS: -I${SRCDIR}/../../third_party/pfsshell/subprojects/iomanX -I${SRCDIR}/../../third_party/pfsshell/subprojects/iomanX/include -I${SRCDIR}/../../third_party/pfsshell/subprojects/hdlfs
#cgo windows CFLAGS: -DNEED_GETLINE
#cgo linux LDFLAGS: -lpthread
#include <stdlib.h>
extern int pfsshell_c_main(int argc, char **argv);
#include <stdio.h>
static int pfsshell_call(int argc, char **argv) { int r = pfsshell_c_main(argc, argv); fflush(NULL); return r; }
*/
import "C"

import (
	"unsafe"

	_ "github.com/freshness79/ps2hdl-manager/internal/pfsshell/apa"
	_ "github.com/freshness79/ps2hdl-manager/internal/pfsshell/fakesdk"
	_ "github.com/freshness79/ps2hdl-manager/internal/pfsshell/hdlfs"
	_ "github.com/freshness79/ps2hdl-manager/internal/pfsshell/iomanx"
	_ "github.com/freshness79/ps2hdl-manager/internal/pfsshell/pfs"
)

// Main runs pfsshell's original main() with args (args[0] = program name).
// Call it only in a dedicated child process: the C code may call exit().
func Main(args []string) int {
	p := C.malloc(C.size_t(len(args)+1) * C.size_t(unsafe.Sizeof(uintptr(0))))
	a := unsafe.Slice((**C.char)(p), len(args)+1)
	for i, s := range args {
		a[i] = C.CString(s)
	}
	a[len(args)] = nil
	return int(C.pfsshell_call(C.int(len(args)), (**C.char)(p)))
}
