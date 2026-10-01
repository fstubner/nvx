package nvx

import (
	"errors"
	"path/filepath"
	"runtime"
)

// hostIsMusl reports whether the system under root runs musl libc (Alpine)
// rather than glibc, by its dynamic loader.
//
// A musl loader alone is not enough: Debian's `musl` package installs one on a
// glibc system. The glibc loader being absent is the half that decides, and it
// is what the downloaded binary asks for.
func hostIsMusl(root string) bool {
	has := func(pattern string) bool {
		for _, dir := range []string{"lib", "lib64", "usr/lib", "usr/lib64"} {
			if m, _ := filepath.Glob(filepath.Join(root, dir, pattern)); len(m) > 0 {
				return true
			}
		}
		return false
	}
	return has("ld-musl-*") && !has("ld-linux*")
}

// refuseGlibcBuildOnMusl stops an install of a build that cannot run here.
//
// nvx downloads the glibc build of Node.js and Bun. On Alpine that unpacks and
// reports success, and then `node` fails with "fork/exec ...: no such file or
// directory", which names the file that exists rather than the loader it needs.
// nvx has no musl download, so the install is refused with the cause named.
func refuseGlibcBuildOnMusl() error {
	if runtime.GOOS != "linux" || !hostIsMusl("/") {
		return nil
	}
	return errors.New("this system uses musl libc (Alpine), and nvx only downloads the glibc build, " +
		"which cannot run here. Install the runtime with the system package manager instead " +
		"(on Alpine: apk add nodejs npm), or use a glibc-based distribution")
}
