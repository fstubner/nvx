package nvx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// What is allowed to sit in the shim directory, and what nvx does about the rest.
//
// ~/.nvx/bin holds the nvx binary and one shim per wrapped command. It is also
// where `corepack enable` used to put its own links. Corepack puts them beside
// the `corepack` it finds first on PATH, and that was nvx's shim, so its yarn and
// pnpm links replaced nvx's yarn and pnpm shims. Measured 2026-10-07 on Linux:
// a bare `yarn install` then ran a dependency's postinstall with no sandbox and
// the script wrote a file into the real HOME. The next `nvx env` wrote nvx's shim
// text through those links and overwrote corepack's own dist/yarn.js and
// dist/pnpm.js inside the Node install, leaving 57 bytes each (186 before). On Windows
// `nvx init-shims` deleted corepack's launchers and `pnpm` then failed with
// "Could not find real executable".
//
// Three rules come out of that.
//
//   - nvx never writes through a link in this directory (writeExecutableFile).
//   - `corepack enable` is pointed somewhere else (corepack_enable.go).
//   - What is already here and is not nvx's gets removed, and nvx says so. The same
//     inspection feeds `nvx doctor`, which reports it without touching anything.

// shimCommandSet is the names nvx wraps, for lookup.
func shimCommandSet() map[string]bool {
	set := map[string]bool{}
	for _, c := range allShimCommands() {
		set[strings.ToLower(c)] = true
	}
	return set
}

// shimScript is the POSIX shim for cmd, which starts nvx under that command's name.
func shimScript(exePath, cmd string) string {
	return fmt.Sprintf("#!/bin/sh\nexec %s shim %s \"$@\"\n", quotePOSIXShell(exePath), quotePOSIXShell(cmd))
}

// shimScriptTarget returns the nvx binary a POSIX shim for cmd starts, and
// whether content is an nvx shim for cmd at all. Any path is accepted, because a
// shim that names an older or a moved nvx is still nvx's own. The first releases
// wrote the command name without quotes, and those count too.
func shimScriptTarget(content, cmd string) (string, bool) {
	const head = "#!/bin/sh\nexec "
	var quoted string
	for _, tail := range []string{
		" shim " + quotePOSIXShell(cmd) + " \"$@\"\n",
		" shim " + cmd + " \"$@\"\n",
	} {
		if len(content) >= len(head)+len(tail) && strings.HasPrefix(content, head) && strings.HasSuffix(content, tail) {
			quoted = content[len(head) : len(content)-len(tail)]
			break
		}
	}
	if len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
		return "", false
	}
	return strings.ReplaceAll(quoted[1:len(quoted)-1], `'"'"'`, "'"), true
}

// foreignShim is something in the shim directory that nvx did not write and that
// stands in the way of a wrapped command.
type foreignShim struct {
	name string
	path string
	// stem is the command the file belongs to. On Windows corepack writes three
	// files for one (pnpm, pnpm.CMD, pnpm.ps1), and they are reported together.
	stem string
	// what is a plain description, such as "a link to ../x/yarn.js".
	what string
	// atShimName is true when it sits at the name of an nvx shim, which it
	// replaced. False for the other names corepack adds (yarnpkg, pnpx, pnx).
	atShimName bool
	corepack   bool
}

// shimDirReport is the read-only inspection of the shim directory.
type shimDirReport struct {
	foreign []foreignShim
	// stale are nvx's own shims that run the wrong nvx. On Windows that is a link
	// to a binary that has since been replaced, and on POSIX a script naming an nvx
	// that is gone.
	stale []string
	// missing are wrapped commands with no shim at all.
	missing []string
}

func (r shimDirReport) healthy() bool {
	return len(r.foreign) == 0 && len(r.stale) == 0 && len(r.missing) == 0
}

// readSmallFile returns the start of a file, enough to recognise a launcher.
func readSmallFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, 8192))
	return string(data)
}

// corepackExtraNames are the names corepack links besides yarn and pnpm. A link
// or launcher with any other name that points into corepack is somebody's own,
// and is left where it is.
var corepackExtraNames = map[string]bool{"yarnpkg": true, "pnpx": true, "pnx": true}

// pointsIntoCorepack reports a path that names a file in corepack's dist
// directory, which is where every link `corepack enable` makes ends up.
func pointsIntoCorepack(target string) bool {
	t := strings.ToLower(filepath.ToSlash(target))
	return strings.Contains(t, "corepack/dist/")
}

// isCorepackLauncher reports a file corepack wrote on Windows. The sh, cmd and
// PowerShell launchers all spell out the path to corepack's dist directory.
func isCorepackLauncher(content string) bool {
	c := strings.ToLower(strings.ReplaceAll(content, `\`, "/"))
	return strings.Contains(c, "corepack/dist")
}

// inspectShimDir looks at the shim directory without changing it.
func inspectShimDir(nvxHome string) shimDirReport {
	shimDir := filepath.Join(nvxHome, "bin")
	rep := shimDirReport{foreign: foreignShims(shimDir)}
	if runtime.GOOS == "windows" {
		rep.stale, rep.missing = windowsShimState(shimDir)
	} else {
		rep.stale, rep.missing = posixShimState(shimDir)
	}
	// allShimCommands walks a map, so its order changes from run to run.
	sort.Strings(rep.stale)
	sort.Strings(rep.missing)
	return rep
}

// foreignShims lists what is in dir and is not nvx's.
func foreignShims(dir string) []foreignShim {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := shimCommandSet()
	var out []foreignShim
	for _, e := range entries {
		var f *foreignShim
		if runtime.GOOS == "windows" {
			f = foreignWindowsEntry(dir, e.Name(), names)
		} else {
			f = foreignPosixEntry(dir, e.Name(), names)
		}
		if f != nil {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func foreignPosixEntry(dir, name string, shimNames map[string]bool) *foreignShim {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	atShimName := shimNames[name]
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, _ := os.Readlink(path)
		corepack := pointsIntoCorepack(target)
		if !atShimName && !(corepack && corepackExtraNames[name]) {
			return nil // nvx's own binary may be a link, and a link elsewhere is not ours to judge
		}
		return &foreignShim{name: name, path: path, stem: name, what: "a link to " + target, atShimName: atShimName, corepack: corepack}
	case !atShimName:
		return nil
	case info.Mode().IsRegular():
		content := readSmallFile(path)
		if _, ok := shimScriptTarget(content, name); ok {
			return nil
		}
		return &foreignShim{name: name, path: path, stem: name, what: "a file nvx did not write", atShimName: true,
			corepack: isCorepackLauncher(content)}
	default:
		what := "a special file"
		if info.IsDir() {
			what = "a directory"
		}
		return &foreignShim{name: name, path: path, stem: name, what: what, atShimName: true}
	}
}

// foreignWindowsEntry covers the files corepack writes on Windows. nvx's own
// shims there are <cmd>.exe, so the extensionless, .cmd and .ps1 names are all
// someone else's now. What an older nvx wrote under those names is its own and
// is replaced without comment.
func foreignWindowsEntry(dir, name string, shimNames map[string]bool) *foreignShim {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != "" && ext != ".cmd" && ext != ".ps1" {
		return nil
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	content := readSmallFile(path)
	if strings.Contains(content, " shim ") && strings.Contains(strings.ToLower(content), "nvx") {
		return nil
	}
	stem := name
	if ext != "" {
		stem = strings.TrimSuffix(name, filepath.Ext(name))
	}
	atShimName := shimNames[strings.ToLower(stem)]
	corepack := isCorepackLauncher(content)
	if !atShimName && !(corepack && corepackExtraNames[strings.ToLower(stem)]) {
		return nil
	}
	what := "files nvx did not write"
	if corepack {
		what = "launchers that corepack wrote"
	}
	return &foreignShim{name: name, path: path, stem: stem, what: what, atShimName: atShimName, corepack: corepack}
}

// posixShimState reports the stale and missing shims of a POSIX shim directory.
func posixShimState(dir string) (stale, missing []string) {
	for _, cmd := range allShimCommands() {
		path := filepath.Join(dir, cmd)
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, cmd)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue // foreignShims reports it
		}
		exe, ok := shimScriptTarget(readSmallFile(path), cmd)
		if !ok || !filepath.IsAbs(exe) {
			continue
		}
		if st, err := os.Stat(exe); err != nil || st.IsDir() {
			stale = append(stale, cmd)
		}
	}
	return stale, missing
}

// windowsShimState compares each <cmd>.exe with the nvx.exe beside it.
//
// An upgrade moves a new nvx.exe over the old one, and every shim, being a hard
// link, stays on the old file. Doctor said "intercepting commands correctly"
// meanwhile until the next shell start relinked them. Measured 2026-10-07, the
// shims were 12,467,712 bytes beside an nvx.exe of 12,492,288.
func windowsShimState(dir string) (stale, missing []string) {
	nvxExe := filepath.Join(dir, "nvx.exe")
	haveNvx := regularFileExists(nvxExe)
	for _, cmd := range allShimCommands() {
		shim := filepath.Join(dir, cmd+".exe")
		if _, err := os.Stat(shim); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, cmd)
			}
			continue
		}
		if haveNvx && !sameExistingFile(nvxExe, shim) && !sameSizeAndTime(nvxExe, shim) {
			stale = append(stale, cmd)
		}
	}
	return stale, missing
}

// clearForeignShims removes what foreignShims finds and says what it removed.
//
// Removing is the repair. The nvx shim goes in next, and whatever was here ran
// its program outside nvx. When the thing removed was corepack's, the message
// says how to get yarn and pnpm back, since that is what the person was after.
func clearForeignShims(shimDir string) {
	var order []string
	groups := map[string][]foreignShim{}
	for _, f := range foreignShims(shimDir) {
		if err := os.Remove(f.path); err != nil {
			// Another shell starting at the same moment removed it first.
			if !os.IsNotExist(err) {
				LogWarn("Could not remove %s from nvx's shim directory (%v). It holds %s, which runs outside nvx.", f.name, err, f.what)
			}
			continue
		}
		if _, seen := groups[f.stem]; !seen {
			order = append(order, f.stem)
		}
		groups[f.stem] = append(groups[f.stem], f)
	}
	corepack := false
	for _, stem := range order {
		group := groups[stem]
		names := make([]string, len(group))
		for i, f := range group {
			names[i] = f.name
			corepack = corepack || f.corepack
		}
		if group[0].atShimName {
			LogWarn("Replaced %s in nvx's shim directory, where %s stood in for nvx's shim and ran outside nvx.", strings.Join(names, ", "), group[0].what)
		} else {
			LogWarn("Removed %s from nvx's shim directory, where %s ran outside nvx.", strings.Join(names, ", "), group[0].what)
		}
	}
	if corepack {
		LogInfo("Run 'corepack enable' to set up yarn and pnpm again. nvx now puts their links next to the Node.js that has corepack, where it can still contain them.")
	}
}

// ensureShims writes the shims when any is missing or in the way. The installers
// do it, and so do `nvx install`, `use` and `default`, so a Dockerfile or a CI
// job that put the nvx binary in place by hand still ends up with shims.
//
// Quiet when everything is as it should be, which is nearly always.
func ensureShims(nvxHome string) {
	rep := inspectShimDir(nvxHome)
	if rep.healthy() {
		return
	}
	if err := generateShims(nvxHome); err != nil {
		LogWarn("Could not write nvx's shims: %v", err)
		return
	}
	if len(rep.missing) == 0 {
		return
	}
	shimDir := shimDirPath(nvxHome)
	LogInfo("Wrote nvx's shims to %s.", shimDir)
	if !diagnosePath(os.Getenv("PATH"), nvxHome, nil).shimDirOnPath {
		LogInfo("Put that directory first on PATH, or run 'nvx doctor --fix', so node and npm run through nvx.")
	}
}

// replaceFileAtomically puts data at path with the given permissions, as a new
// file in the same directory renamed over the old name. Whatever is at path, a
// link included, is replaced and not opened, and no reader sees a half-written file.
// A file that already holds data under those permissions is left alone, since
// `nvx env` runs this for every shim at every shell start.
func replaceFileAtomically(path string, data []byte, perm os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm() == perm {
		if current, rerr := os.ReadFile(path); rerr == nil && string(current) == string(data) {
			return nil
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nvx-shim-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil && cerr == nil {
		werr = os.Chmod(name, perm) // #nosec G302 -- generated shim wrappers must be executable by the owner.
	}
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// formatShimFiles is doctor's account of what is wrong in the shim directory, or
// "" when nothing is.
func formatShimFiles(rep doctorReport) string {
	var b strings.Builder
	if len(rep.shimFiles.foreign) > 0 {
		b.WriteString("  [FAIL] the shim directory holds files that are not nvx's shims:\n")
		for _, f := range rep.shimFiles.foreign {
			fmt.Fprintf(&b, "         - %s (%s)\n", f.name, f.what)
		}
		b.WriteString("         They run their programs outside nvx, so nothing they start is contained.\n")
		b.WriteString("         Fix: nvx init-shims removes them and writes nvx's shims\n")
	}
	if len(rep.shimFiles.stale) > 0 {
		b.WriteString("  [FAIL] these shims run an older nvx, or one that is gone: " + strings.Join(rep.shimFiles.stale, ", ") + "\n")
		b.WriteString("         Fix: nvx init-shims\n")
	}
	if missing := rep.otherMissingShims(); len(missing) > 0 {
		b.WriteString("  [FAIL] no shim for: " + strings.Join(missing, ", ") + "\n")
		b.WriteString("         Fix: nvx init-shims\n")
	}
	return b.String()
}
