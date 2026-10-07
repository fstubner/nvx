package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A project the way an install leaves it, the working directory for the rest of
// the test. Each bin is written as npm writes it on this system: the script
// <name>, and on Windows <name>.cmd and <name>.ps1 beside it.
func binProject(t *testing.T, manifest string, bins ...string) string {
	t.Helper()
	dir := tempDir(t)
	if manifest == "" {
		manifest = `{"name":"fixture","private":true}`
	}
	// Neither this machine's user .npmrc nor its npm_config_package may decide
	// the answer, because either can make npx fetch.
	t.Setenv("npm_config_userconfig", filepath.Join(dir, "no-user.npmrc"))
	t.Setenv("npm_config_package", "")
	writeTestFile(t, filepath.Join(dir, "package.json"), manifest, 0o644)
	for _, name := range bins {
		writeNpmBin(t, filepath.Join(dir, "node_modules", ".bin"), name)
	}
	inProjectDir(t, dir)
	return dir
}

func writeTestFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func writeNpmBin(t *testing.T, binDir, name string) {
	t.Helper()
	writeTestFile(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 0\n", 0o755)
	if runtime.GOOS == "windows" {
		writeTestFile(t, filepath.Join(binDir, name+".cmd"), "@echo off\r\n", 0o644)
		writeTestFile(t, filepath.Join(binDir, name+".ps1"), "exit 0\r\n", 0o644)
	}
}

type classRow struct {
	name string
	cmd  string
	args []string
	want invocationClass
}

func checkClasses(t *testing.T, rows []classRow) {
	t.Helper()
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyInvocation(tc.cmd, tc.args); got != tc.want {
				t.Errorf("classifyInvocation(%q, %q) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}

// An npx, npm exec or bunx line that names a tool already in node_modules/.bin
// runs that file and fetches nothing, so it is classified as `npm run` is. Every
// npx was an ad-hoc tool before.
func TestAToolAlreadyInNodeModulesBinIsYourCode(t *testing.T) {
	binProject(t, "", "vitest", "tsx", "prisma", "husky", "_mocha", "ts-node-dev")
	checkClasses(t, []classRow{
		{"npx", "npx", []string{"vitest", "run"}, classYourCode},
		{"npx tsx", "npx", []string{"tsx", "src/index.ts"}, classYourCode},
		{"npx prisma", "npx", []string{"prisma", "migrate", "dev"}, classYourCode},
		{"npx husky", "npx", []string{"husky"}, classYourCode},
		{"npx with no arguments for the tool", "npx", []string{"vitest"}, classYourCode},
		{"npx with a leading underscore", "npx", []string{"_mocha"}, classYourCode},
		{"npx with dashes and a dot", "npx", []string{"ts-node-dev"}, classYourCode},
		{"npx -y", "npx", []string{"-y", "vitest"}, classYourCode},
		{"npx --yes", "npx", []string{"--yes", "vitest"}, classYourCode},
		{"npx --yes=false", "npx", []string{"--yes=false", "vitest"}, classYourCode},
		{"npx --no", "npx", []string{"--no", "vitest"}, classYourCode},
		{"npx --no-install", "npx", []string{"--no-install", "vitest"}, classYourCode},
		{"npx -q", "npx", []string{"-q", "vitest"}, classYourCode},
		{"npx --quiet --yes", "npx", []string{"--quiet", "--yes", "vitest"}, classYourCode},
		{"npx --silent", "npx", []string{"--silent", "vitest"}, classYourCode},
		{"npx after --", "npx", []string{"--", "vitest", "run"}, classYourCode},
		{"npx tool arguments that look like flags", "npx", []string{"vitest", "--package", "x", "-c", "y"}, classYourCode},
		{"npm exec with positional tool arguments", "npm", []string{"exec", "vitest", "run", "src"}, classYourCode},
		{"npm exec with a known switch after the name", "npm", []string{"exec", "vitest", "--yes", "run"}, classYourCode},
		{"npm exec with flags for the tool after --", "npm", []string{"exec", "--", "vitest", "--package=x", "-c", "y"}, classYourCode},
		{"bunx tool arguments that look like flags", "bunx", []string{"vitest", "--package=x", "--registry", "y"}, classYourCode},
		{"bun x tool arguments that look like flags", "bun", []string{"x", "vitest", "--package", "x", "--cwd", ".."}, classYourCode},
		{"npm exec", "npm", []string{"exec", "vitest"}, classYourCode},
		{"npm exec after --", "npm", []string{"exec", "--", "vitest"}, classYourCode},
		{"npm exec --yes", "npm", []string{"exec", "--yes", "vitest"}, classYourCode},
		{"npm --yes exec", "npm", []string{"--yes", "exec", "vitest"}, classYourCode},
		{"npm x", "npm", []string{"x", "vitest"}, classYourCode},
		{"npm exe, which npm reads as exec", "npm", []string{"exe", "vitest"}, classYourCode},
		{"NPX in capitals", "NPX", []string{"vitest"}, classYourCode},
		{"bunx", "bunx", []string{"vitest"}, classYourCode},
		{"bunx --bun", "bunx", []string{"--bun", "vitest"}, classYourCode},
		{"bunx --no-install", "bunx", []string{"--no-install", "vitest"}, classYourCode},
		{"bun x", "bun", []string{"x", "vitest"}, classYourCode},
		{"bun --bun x", "bun", []string{"--bun", "x", "vitest"}, classYourCode},
	})
}

// Anything that could fetch stays an ad-hoc tool, even when a file of that name
// is in node_modules/.bin. The first row is the control: the same project, with
// the tool run by its name.
func TestAnythingThatCouldFetchStaysAnAdHocTool(t *testing.T) {
	// Files named like specs. Measured 2026-10-07 with npm 11.19.0, `npx zzz@1`
	// ran node_modules/.bin/zzz@1 and made no request, because npm looks for the
	// argument as a file name before it reads it as a spec. nvx does not read such
	// a name as a tool.
	root := binProject(t, "", "vitest", "vitest@1", "vitest@latest")
	writeNpmBin(t, filepath.Join(root, "node_modules", ".bin"), "..vitest")
	checkClasses(t, []classRow{
		{"control: the tool by its name", "npx", []string{"vitest"}, classYourCode},
		{"a tool that is not installed", "npx", []string{"cowsay", "hi"}, classAdHocTool},
		{"no tool named", "npx", nil, classAdHocTool},
		{"only flags", "npx", []string{"-y"}, classAdHocTool},
		{"a version", "npx", []string{"vitest@1"}, classAdHocTool},
		{"the exact installed version", "npx", []string{"vitest@1.0.0"}, classAdHocTool},
		{"a tag", "npx", []string{"vitest@latest"}, classAdHocTool},
		{"a scoped name", "npx", []string{"@vitest/ui"}, classAdHocTool},
		{"a git shorthand", "npx", []string{"user/vitest"}, classAdHocTool},
		{"a hosted-repository spec", "npx", []string{"github:user/vitest"}, classAdHocTool},
		{"a URL", "npx", []string{"https://example.com/vitest.tgz"}, classAdHocTool},
		{"a relative path", "npx", []string{"./vitest"}, classAdHocTool},
		{"a parent path", "npx", []string{"../vitest"}, classAdHocTool},
		{"a Windows path", "npx", []string{`.\vitest`}, classAdHocTool},
		{"the folder itself", "npx", []string{"."}, classAdHocTool},
		{"a name that starts with a dot", "npx", []string{"..vitest"}, classAdHocTool},
		{"a name ending in a dot", "npx", []string{"vitest."}, classAdHocTool},
		{"a stream name", "npx", []string{"vitest:stream"}, classAdHocTool},
		{"-p", "npx", []string{"-p", "vitest", "vitest"}, classAdHocTool},
		{"--package", "npx", []string{"--package", "vitest", "vitest"}, classAdHocTool},
		{"--package=", "npx", []string{"--package=vitest", "vitest"}, classAdHocTool},
		{"-c", "npx", []string{"-c", "vitest run"}, classAdHocTool},
		{"--call=", "npx", []string{"--call=vitest run", "vitest"}, classAdHocTool},
		{"--call= with no name", "npx", []string{"--call=vitest run"}, classAdHocTool},
		{"a flag that is not known", "npx", []string{"--foo", "vitest"}, classAdHocTool},
		{"a value flag that is not known", "npx", []string{"--registry", "https://example.com", "vitest"}, classAdHocTool},
		{"--prefix", "npx", []string{"--prefix", "../other", "vitest"}, classAdHocTool},
		{"--workspace", "npx", []string{"--workspace=web", "vitest"}, classAdHocTool},
		{"-w", "npx", []string{"-w", "web", "vitest"}, classAdHocTool},
		{"--strict is read before the command only", "npx", []string{"--strict", "vitest"}, classAdHocTool},
		{"--no-sandbox is read before the command only", "npx", []string{"--no-sandbox", "vitest"}, classAdHocTool},
		{"npm exec -p", "npm", []string{"exec", "-p", "vitest", "vitest"}, classAdHocTool},
		{"npm exec --package after the name, which npm reads", "npm", []string{"exec", "vitest", "--package=evil"}, classAdHocTool},
		{"npm exec --package and its value after the name", "npm", []string{"exec", "vitest", "--package", "evil", "run"}, classAdHocTool},
		{"npm exec --registry after the name", "npm", []string{"exec", "vitest", "--registry=https://example.com"}, classAdHocTool},
		{"npm exec --prefix after the name", "npm", []string{"exec", "vitest", "--prefix", "/elsewhere"}, classAdHocTool},
		{"npm exec -c after the name", "npm", []string{"exec", "vitest", "-c", "x"}, classAdHocTool},
		{"npm x --package after the name", "npm", []string{"x", "vitest", "--package=evil"}, classAdHocTool},
		{"npm exec a tool flag before the --", "npm", []string{"exec", "vitest", "--run", "--", "x"}, classAdHocTool},
		{"npm exec a version", "npm", []string{"exec", "vitest@1"}, classAdHocTool},
		{"npm exec --workspaces", "npm", []string{"exec", "--workspaces", "vitest"}, classAdHocTool},
		{"npm exec an unknown flag first", "npm", []string{"--foo", "exec", "vitest"}, classAdHocTool},
		{"npm create", "npm", []string{"create", "vitest"}, classAdHocTool},
		{"npm init an initializer", "npm", []string{"init", "vitest"}, classAdHocTool},
		{"pnpm dlx", "pnpm", []string{"dlx", "vitest"}, classAdHocTool},
		{"yarn dlx", "yarn", []string{"dlx", "vitest"}, classAdHocTool},
		{"bun create", "bun", []string{"create", "vitest"}, classAdHocTool},
		{"bunx a version", "bunx", []string{"vitest@1"}, classAdHocTool},
		{"bunx -p", "bunx", []string{"-p", "vitest", "vitest"}, classAdHocTool},
		{"bunx --package=", "bunx", []string{"--package=vitest", "vitest"}, classAdHocTool},
		{"bunx a flag npx knows", "bunx", []string{"-y", "vitest"}, classAdHocTool},
		{"npx a flag bunx knows", "npx", []string{"--bun", "vitest"}, classAdHocTool},
		{"bun x a version", "bun", []string{"x", "vitest@1"}, classAdHocTool},
	})
}

// The runners that are not package-manager commands, and the verbs that are not
// exec, are read as before.
func TestOtherCommandsAreNotReadAsAToolRun(t *testing.T) {
	binProject(t, "", "vitest")
	checkClasses(t, []classRow{
		{"npm run", "npm", []string{"run", "vitest"}, classYourCode},
		{"npm run of a script called exec", "npm", []string{"run", "exec", "vitest"}, classYourCode},
		{"bare vitest through its shim", "vitest", []string{"run"}, classYourCode},
		{"npm install", "npm", []string{"install", "vitest"}, classInstall},
		{"corepack npx", "corepack", []string{"npx", "vitest"}, classYourCode},
		{"corepack npx of a tool that is not installed", "corepack", []string{"npx", "cowsay"}, classAdHocTool},
	})
}

// A command run from a folder below the project root is looked up where npm
// looks, in the project's node_modules/.bin.
func TestAToolIsFoundFromAFolderBelowTheProject(t *testing.T) {
	root := binProject(t, "", "vitest")
	deep := filepath.Join(root, "src", "components")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	inProjectDir(t, deep)
	checkClasses(t, []classRow{
		{"npx", "npx", []string{"vitest"}, classYourCode},
		{"npx of a tool that is not installed", "npx", []string{"cowsay"}, classAdHocTool},
	})

	// A folder with its own package.json is its own project, and npm looks in
	// what is installed there before it looks above.
	nested := filepath.Join(root, "tools", "gen")
	writeTestFile(t, filepath.Join(nested, "package.json"), `{"name":"gen","private":true}`, 0o644)
	inProjectDir(t, nested)
	checkClasses(t, []classRow{
		{"a nested project does not use the outer one's tools", "npx", []string{"vitest"}, classAdHocTool},
	})
	writeNpmBin(t, filepath.Join(nested, "node_modules", ".bin"), "vitest")
	checkClasses(t, []classRow{
		{"a nested project's own tool", "npx", []string{"vitest"}, classYourCode},
	})
}

// In a workspace, what the members share is installed in the root. A project
// that the root's workspaces field does not list is not part of the workspace.
func TestAWorkspaceMemberFindsTheRootsTools(t *testing.T) {
	root := tempDir(t)
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"root","private":true,"workspaces":["packages/*"]}`, 0o644)
	writeNpmBin(t, filepath.Join(root, "node_modules", ".bin"), "vitest")
	web := filepath.Join(root, "packages", "web")
	writeTestFile(t, filepath.Join(web, "package.json"), `{"name":"web"}`, 0o644)
	writeTestFile(t, filepath.Join(web, "src", "app.ts"), "", 0o644)
	stranger := filepath.Join(root, "vendor", "other")
	writeTestFile(t, filepath.Join(stranger, "package.json"), `{"name":"other"}`, 0o644)

	inProjectDir(t, web)
	checkClasses(t, []classRow{
		{"a member", "npx", []string{"vitest"}, classYourCode},
		{"a member's tool that is not installed", "npx", []string{"cowsay"}, classAdHocTool},
	})
	inProjectDir(t, filepath.Join(web, "src"))
	checkClasses(t, []classRow{
		{"a folder in a member", "npx", []string{"vitest"}, classYourCode},
	})
	inProjectDir(t, stranger)
	checkClasses(t, []classRow{
		{"a project the root does not list", "npx", []string{"vitest"}, classAdHocTool},
	})

	// The object form, and a pattern that reaches the member by a longer path.
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":{"packages":["vendor/*"]}}`, 0o644)
	checkClasses(t, []classRow{
		{"a member listed in the object form", "npx", []string{"vitest"}, classYourCode},
	})
	inProjectDir(t, web)
	checkClasses(t, []classRow{
		{"a member the root no longer lists", "npx", []string{"vitest"}, classAdHocTool},
	})

	// npm reads the root's package.json for a bin of that name too.
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":["packages/*"],"bin":{"vitest":"./cli.js"}}`, 0o644)
	checkClasses(t, []classRow{
		{"a name the root's package.json lists under bin", "npx", []string{"vitest"}, classAdHocTool},
	})
}

// A project whose own package.json lists the name under bin is not run from
// node_modules/.bin. From npm 9.6.3 npx installs the project into its cache and
// runs it there.
func TestAToolTheProjectItselfProvidesIsAnAdHocRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		want     invocationClass
	}{
		{"bin object naming the tool", `{"name":"p","bin":{"vitest":"./cli.js"}}`, classAdHocTool},
		{"bin object naming another tool", `{"name":"p","bin":{"other":"./cli.js"}}`, classYourCode},
		{"bin path named for the package", `{"name":"vitest","bin":"./cli.js"}`, classAdHocTool},
		{"bin path named for a scoped package", `{"name":"@acme/vitest","bin":"./cli.js"}`, classAdHocTool},
		{"bin path of another package", `{"name":"other","bin":"./cli.js"}`, classYourCode},
		{"bin path with no package name", `{"bin":"./cli.js"}`, classAdHocTool},
		{"directories.bin names every file in a folder", `{"name":"p","directories":{"bin":"bin"}}`, classAdHocTool},
		{"no bin", `{"name":"p"}`, classYourCode},
		{"null bin", `{"name":"p","bin":null}`, classYourCode},
		{"a package.json that does not parse", `{"name":`, classAdHocTool},
		{"a package.json with a byte order mark", "\xef\xbb\xbf" + `{"name":"p"}`, classYourCode},
		{"a package.json with a byte order mark and the bin", "\xef\xbb\xbf" + `{"name":"p","bin":{"vitest":"x"}}`, classAdHocTool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binProject(t, tc.manifest, "vitest")
			if got := classifyInvocation("npx", []string{"vitest"}); got != tc.want {
				t.Errorf("classifyInvocation(npx vitest) = %v, want %v", got, tc.want)
			}
		})
	}
}

// npm looks for the file <name> itself. With only <name>.cmd in .bin it fetched,
// measured with npm 8.12.1 to 11.19.0 on Windows.
func TestOnlyTheScriptCountsForNpm(t *testing.T) {
	root := binProject(t, "")
	writeTestFile(t, filepath.Join(root, "node_modules", ".bin", "vitest.cmd"), "@echo off\r\n", 0o644)
	writeTestFile(t, filepath.Join(root, "node_modules", ".bin", "vitest.ps1"), "exit 0\r\n", 0o644)
	checkClasses(t, []classRow{
		{"only the launchers", "npx", []string{"vitest"}, classAdHocTool},
	})
	writeTestFile(t, filepath.Join(root, "node_modules", ".bin", "vitest"), "#!/bin/sh\n", 0o644)
	checkClasses(t, []classRow{
		{"the script too", "npx", []string{"vitest"}, classYourCode},
	})
}

// A folder named like a tool is not a tool.
func TestAFolderInNodeModulesBinIsNotATool(t *testing.T) {
	root := binProject(t, "")
	folder := filepath.Join(root, "node_modules", ".bin", "vitest")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	checkClasses(t, []classRow{
		{"a folder", "npx", []string{"vitest"}, classAdHocTool},
	})
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	writeNpmBin(t, filepath.Join(root, "node_modules", ".bin"), "vitest")
	checkClasses(t, []classRow{
		{"control: the same name as a file", "npx", []string{"vitest"}, classYourCode},
	})
}

// Bun looks in .bin the way a shell looks in PATH. Measured with bun 1.4.2 on
// Windows, bunx ran <name>.exe, <name>.cmd and <name>.bat there, and fetched
// when .bin held only <name>.ps1, <name>.bunx or the script <name>. On Unix the
// source of bun's lookup (bun_which::which) takes an executable file, and it was
// not run there.
func TestBunxFindsWhatBunFinds(t *testing.T) {
	root := binProject(t, "")
	bin := filepath.Join(root, "node_modules", ".bin")
	rows := func(want invocationClass) []classRow {
		return []classRow{{"bunx", "bunx", []string{"mytool"}, want}, {"bun x", "bun", []string{"x", "mytool"}, want}}
	}

	writeTestFile(t, filepath.Join(bin, "mytool"), "#!/bin/sh\n", 0o644)
	checkClasses(t, rows(classAdHocTool)) // not executable on Unix, a script on Windows
	if runtime.GOOS == "windows" {
		for _, ext := range []string{".bunx", ".ps1"} {
			writeTestFile(t, filepath.Join(bin, "mytool"+ext), "x", 0o644)
		}
		checkClasses(t, rows(classAdHocTool))
		for _, ext := range []string{".exe", ".cmd", ".bat"} {
			if err := os.WriteFile(filepath.Join(bin, "mytool"+ext), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			checkClasses(t, rows(classYourCode))
			if err := os.Remove(filepath.Join(bin, "mytool"+ext)); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if err := os.Chmod(filepath.Join(bin, "mytool"), 0o755); err != nil {
		t.Fatal(err)
	}
	checkClasses(t, rows(classYourCode))
}

// A bun install on Windows leaves .exe and .bunx, which npx does not look for.
func TestNpxDoesNotRunWhatBunInstalledOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("bun links bins as symlinks outside Windows")
	}
	root := binProject(t, "")
	for _, ext := range []string{".exe", ".bunx"} {
		writeTestFile(t, filepath.Join(root, "node_modules", ".bin", "vitest"+ext), "x", 0o644)
	}
	checkClasses(t, []classRow{
		{"npx", "npx", []string{"vitest"}, classAdHocTool},
		{"bunx", "bunx", []string{"vitest"}, classYourCode},
	})
}

// A link that leads out of the project is not the project's code. A contained
// run can plant one, and so can a trusted tool's profile that outlives the run.
func TestALinkOutOfTheProjectIsAnAdHocRun(t *testing.T) {
	root := binProject(t, "")
	outside := tempDir(t)
	writeTestFile(t, filepath.Join(outside, "cli.js"), "#!/usr/bin/env node\n", 0o755)
	writeTestFile(t, filepath.Join(root, "node_modules", "inside", "cli.js"), "#!/usr/bin/env node\n", 0o755)
	bin := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"inside":  filepath.Join("..", "inside", "cli.js"),
		"outside": filepath.Join(outside, "cli.js"),
		"dangles": filepath.Join("..", "missing", "cli.js"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Skipf("cannot make symlinks here: %v", err)
		}
	}
	checkClasses(t, []classRow{
		{"a link inside the project", "npx", []string{"inside"}, classYourCode},
		{"a link outside the project", "npx", []string{"outside"}, classAdHocTool},
		{"a link to nothing", "npx", []string{"dangles"}, classAdHocTool},
		{"bunx, a link outside the project", "bunx", []string{"outside"}, classAdHocTool},
	})

	// The nearest entry is the one that runs. A link out of the project in the
	// project's own folder is not passed over for a good file in the workspace
	// root.
	parent := tempDir(t)
	writeTestFile(t, filepath.Join(parent, "package.json"), `{"workspaces":["member"]}`, 0o644)
	writeNpmBin(t, filepath.Join(parent, "node_modules", ".bin"), "outside")
	member := filepath.Join(parent, "member")
	writeTestFile(t, filepath.Join(member, "package.json"), `{"name":"member"}`, 0o644)
	if err := os.MkdirAll(filepath.Join(member, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "cli.js"), filepath.Join(member, "node_modules", ".bin", "outside")); err != nil {
		t.Skipf("cannot make symlinks here: %v", err)
	}
	inProjectDir(t, member)
	checkClasses(t, []classRow{
		{"a bad entry nearer than a good one", "npx", []string{"outside"}, classAdHocTool},
	})
}

// A tool the project does not have is run from further up only when the folder
// above holds a workspace the project belongs to. npm walks to the top of the
// drive, and nvx stops at the project and its workspace root.
func TestAFolderAboveTheProjectIsNotSearched(t *testing.T) {
	parent := tempDir(t)
	writeNpmBin(t, filepath.Join(parent, "node_modules", ".bin"), "vitest")
	project := filepath.Join(parent, "app")
	writeTestFile(t, filepath.Join(project, "package.json"), `{"name":"app"}`, 0o644)
	inProjectDir(t, project)
	checkClasses(t, []classRow{
		{"a tool only the folder above has", "npx", []string{"vitest"}, classAdHocTool},
	})
	writeNpmBin(t, filepath.Join(project, "node_modules", ".bin"), "vitest")
	checkClasses(t, []classRow{
		{"control: the tool in the project's own folder", "npx", []string{"vitest"}, classYourCode},
	})
}

// The decision follows the isolation level as `npm run` does: standard runs the
// tool as your code, and strict contains it.
func TestAProjectToolFollowsTheIsolationLevel(t *testing.T) {
	binProject(t, "", "vitest")
	standard := DefaultPolicy()
	strict := DefaultPolicy()
	strict.Isolation.Level = "strict"

	for _, tc := range []struct {
		name    string
		cmd     string
		args    []string
		policy  Policy
		opts    shimOptions
		contain bool
	}{
		{"npm run at standard", "npm", []string{"run", "test"}, standard, shimOptions{}, false},
		{"npx of a project tool at standard", "npx", []string{"vitest"}, standard, shimOptions{}, false},
		{"npm exec of a project tool at standard", "npm", []string{"exec", "vitest"}, standard, shimOptions{}, false},
		{"bunx of a project tool at standard", "bunx", []string{"vitest"}, standard, shimOptions{}, false},
		{"npm run at strict", "npm", []string{"run", "test"}, strict, shimOptions{}, true},
		{"npx of a project tool at strict", "npx", []string{"vitest"}, strict, shimOptions{}, true},
		{"npx of a project tool, nvx --strict", "npx", []string{"vitest"}, standard, shimOptions{strictFlag: true}, true},
		{"npx of a tool to fetch at standard", "npx", []string{"cowsay"}, standard, shimOptions{}, true},
		{"npx of a version at standard", "npx", []string{"vitest@1"}, standard, shimOptions{}, true},
		{"npx -p at standard", "npx", []string{"-p", "vitest", "vitest"}, standard, shimOptions{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSandbox(tc.cmd, tc.args, tc.policy, tc.opts); got != tc.contain {
				t.Errorf("shouldSandbox(%s %q) = %v, want %v", tc.cmd, tc.args, got, tc.contain)
			}
		})
	}
}

// The install-script question was asked about a tool that was already
// installed, and an agent or CI run had no way to answer it, so the run was
// refused. Nothing is fetched, so nothing is asked. A tool that is fetched is
// still asked about, which is the control.
func TestAnInstalledToolIsNotCheckedAsAFetch(t *testing.T) {
	asked := map[string]int{}
	orig := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		asked[pkgName]++
		return "1.0.0", time.Now().Add(-30 * 24 * time.Hour), true, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = orig })

	// Nobody answers, so a question that is asked is refused.
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	prevYes, prevAgent := yesFlag, agentModeFlag
	yesFlag, agentModeFlag = false, false
	t.Cleanup(func() { yesFlag, agentModeFlag = prevYes, prevAgent })

	home := tempDir(t)
	writeTestFile(t, filepath.Join(home, "policy.json"), `{"typosquatting":{"enabled":false}}`, 0o600)
	writeTestFile(t, filepath.Join(home, "popular_packages.json"), `["react"]`, 0o600)
	binProject(t, "", "esbuild")

	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"npx", []string{"esbuild", "--version"}},
		{"npx", []string{"--yes", "esbuild"}},
		{"npm", []string{"exec", "esbuild"}},
		{"bunx", []string{"esbuild"}},
	} {
		code, reason, _ := verifyBeforeRun(verifyRequest{pmCmd: tc.cmd, pmArgs: tc.args, nvxHome: home})
		if code != 0 || len(asked) != 0 {
			t.Errorf("`%s %q` was checked as a fetch (exit %d, %q, looked up %v)", tc.cmd, tc.args, code, reason, asked)
		}
	}

	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"npx", []string{"cowsay"}},
		{"npx", []string{"esbuild@1"}},
		{"npm", []string{"exec", "-p", "esbuild", "esbuild"}},
	} {
		clear(asked)
		code, _, _ := verifyBeforeRun(verifyRequest{pmCmd: tc.cmd, pmArgs: tc.args, nvxHome: home})
		if code == 0 || len(asked) == 0 {
			t.Errorf("`%s %q` that fetches skipped the install-script question (exit %d, looked up %v)", tc.cmd, tc.args, code, asked)
		}
	}
}

// npm and bun start from the real folder, so a cd through a link is judged where
// it leads. Getwd answers with the path the shell used.
func TestACdThroughALinkIsJudgedWhereItReallyIs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows process keeps the path it was given")
	}
	base := tempDir(t)
	project := filepath.Join(base, "project")
	other := filepath.Join(base, "other")
	writeTestFile(t, filepath.Join(project, "package.json"), `{"name":"project"}`, 0o644)
	writeNpmBin(t, filepath.Join(project, "node_modules", ".bin"), "vitest")
	writeTestFile(t, filepath.Join(project, "src", "app.ts"), "", 0o644)
	writeTestFile(t, filepath.Join(other, "package.json"), `{"name":"other"}`, 0o644)
	writeTestFile(t, filepath.Join(other, "inner", "file.txt"), "", 0o644)
	if err := os.Symlink(filepath.Join(other, "inner"), filepath.Join(project, "link")); err != nil {
		t.Skipf("cannot make symlinks here: %v", err)
	}
	if err := os.Symlink(project, filepath.Join(base, "alias")); err != nil {
		t.Skipf("cannot make symlinks here: %v", err)
	}

	cd := func(dir string) {
		inProjectDir(t, dir)
		t.Setenv("PWD", dir)
	}
	cd(filepath.Join(base, "alias", "src"))
	checkClasses(t, []classRow{
		{"a folder reached through a link to the project", "npx", []string{"vitest"}, classYourCode},
	})
	// Under the project's own path, but really inside another project that has
	// no tools. Read upward by the path typed, the folder above is the project.
	cd(filepath.Join(project, "link"))
	checkClasses(t, []classRow{
		{"a link in the project that leads into another project", "npx", []string{"vitest"}, classAdHocTool},
	})
}

// npm exec fetches the packages a `package` setting names, whatever the command
// line says. Measured 2026-10-07 with npm 11.19.0 and 1.0.0 installed,
// `package=fakecli@2.0.0` in the project's .npmrc, and npm_config_package in the
// environment, each made `npx fakecli` fetch 2.0.0 and run it. A contained
// install can write the project's .npmrc. Bun does not read the setting.
func TestAPackageSettingMakesNpmFetch(t *testing.T) {
	root := binProject(t, "", "vitest")
	npmrc := filepath.Join(root, ".npmrc")
	userrc := filepath.Join(root, "user.npmrc")
	// A user file that nothing writes, so the machine's own does not count.
	t.Setenv("npm_config_userconfig", userrc)
	t.Setenv("npm_config_package", "")

	rows := func(npm invocationClass) []classRow {
		return []classRow{
			{"npx", "npx", []string{"vitest"}, npm},
			{"npm exec", "npm", []string{"exec", "vitest"}, npm},
			{"bunx", "bunx", []string{"vitest"}, classYourCode},
		}
	}
	checkClasses(t, rows(classYourCode))

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"package in the project's .npmrc", func(t *testing.T) {
			writeTestFile(t, npmrc, "registry=https://example.com/\npackage=vitest@2\n", 0o644)
		}},
		{"package[] in the project's .npmrc", func(t *testing.T) {
			writeTestFile(t, npmrc, "package[]=vitest@2\n", 0o644)
		}},
		{"package in the user's .npmrc", func(t *testing.T) {
			writeTestFile(t, userrc, "package=vitest@2\n", 0o644)
		}},
		{"npm_config_package", func(t *testing.T) {
			t.Setenv("npm_config_package", "vitest@2")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(npmrc)
			_ = os.Remove(userrc)
			t.Setenv("npm_config_package", "")
			tc.setup(t)
			checkClasses(t, rows(classAdHocTool))
		})
	}

	// Other settings are not a package, and a comment is not a setting.
	_ = os.Remove(userrc)
	t.Setenv("npm_config_package", "")
	writeTestFile(t, npmrc, "registry=https://example.com/\nloglevel=error\n; package=vitest@2\n", 0o644)
	checkClasses(t, rows(classYourCode))
}
