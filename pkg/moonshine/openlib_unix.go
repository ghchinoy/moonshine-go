//go:build darwin || freebsd || linux || netbsd

package moonshine

import "github.com/ebitengine/purego"

// openLibrary dlopen's a shared library at path. purego.Dlopen/RTLD_* only
// build on this set of Unix platforms (see purego's dlfcn.go build tag) --
// Windows needs a different mechanism entirely, hence this file being
// split out per-OS rather than calling purego.Dlopen directly from lib.go.
func openLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
