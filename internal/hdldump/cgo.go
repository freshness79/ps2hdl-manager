// Package hdldump embeds hdl_dump 0.9.2 (ps2homebrew/hdl-dump, GPL-2.0).
// The original C sources are vendored unmodified in third_party/hdl-dump.
package hdldump

/*
#cgo CFLAGS: -Wno-error=implicit-function-declaration -Wno-error=int-conversion -Wno-error=incompatible-pointer-types
#cgo darwin CFLAGS: -Wno-error=incompatible-function-pointer-types
#cgo CFLAGS: -w -O2 -Dmain=hdldump_c_main -DVER_MAJOR=0 -DVER_MINOR=9 -DVER_PATCH=2 -DNDEBUG
#cgo CFLAGS: -I${SRCDIR}/../../third_party/hdl-dump
#cgo !windows CFLAGS: -D_GNU_SOURCE -D_BUILD_UNIX
#cgo windows CFLAGS: -D_BUILD_WIN32 -D_WIN32_WINNT=0x0500
#cgo windows LDFLAGS: -lwsock32 -lwinmm -lws2_32
#cgo linux LDFLAGS: -lpthread
#include <stdlib.h>
extern int hdldump_c_main(int argc, char **argv);
#include <stdio.h>
static int hdldump_call(int argc, char **argv) { int r = hdldump_c_main(argc, argv); fflush(NULL); return r; }
extern int hdl_batch_main(int argc, char **argv);
static int hdlbatch_call(int argc, char **argv) { int r = hdl_batch_main(argc, argv); fflush(NULL); return r; }
*/
import "C"

import "unsafe"

// Main runs hdl_dump's original main() with args (args[0] = program name).
// Call it only in a dedicated child process: the C code calls exit().
func Main(args []string) int {
	argv := cArgv(args)
	return int(C.hdldump_call(C.int(len(args)), argv))
}

// BatchMain runs the batch modifier (batch.c) with args (args[0] = program name).
func BatchMain(args []string) int {
	return int(C.hdlbatch_call(C.int(len(args)), cArgv(args)))
}

func cArgv(args []string) **C.char {
	p := C.malloc(C.size_t(len(args)+1) * C.size_t(unsafe.Sizeof(uintptr(0))))
	a := unsafe.Slice((**C.char)(p), len(args)+1)
	for i, s := range args {
		a[i] = C.CString(s)
	}
	a[len(args)] = nil
	return (**C.char)(p)
}
