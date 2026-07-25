package moonshine

import "syscall"

// openLibrary loads a DLL at path. purego.Dlopen/RTLD_* don't exist on
// Windows at all (see purego's dlfcn.go build tag, which excludes it) --
// this mirrors purego's own recommended pattern for this platform (see
// examples/libc/main_windows.go in the purego module): syscall.LoadLibrary
// avoids an extra dependency; golang.org/x/sys/windows.NewLazySystemDLL is
// purego's own suggested alternative for production use if that's ever
// warranted here.
func openLibrary(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	return uintptr(h), err
}

// crtHandle loads the Universal C Runtime so free() can be resolved from
// it. Unlike Unix, Windows has no RTLD_DEFAULT equivalent (an implicitly-
// already-loaded libc) -- the CRT has to be loaded explicitly first.
//
// NOTE: this is unverified on real Windows hardware as of this writing --
// moonshine itself has no Windows shared library release yet (only a
// static moonshine.lib; see bd issue moonshine-go-hbq), so there is
// currently no real moonshine.dll to load and free() memory from on this
// platform to test against. ucrtbase.dll is the modern (Windows 10+),
// standard C runtime DLL that MSVC-built binaries dynamically link against
// by default, which should match, but revisit this once a real Windows
// libmoonshine exists and can be smoke-tested (see internal/moonshine's
// smoke_test.go).
func crtHandle() (uintptr, error) {
	h, err := syscall.LoadLibrary("ucrtbase.dll")
	return uintptr(h), err
}
