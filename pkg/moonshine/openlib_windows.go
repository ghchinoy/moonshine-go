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
