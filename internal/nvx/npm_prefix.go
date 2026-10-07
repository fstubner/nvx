package nvx

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// One home for global npm packages, whichever shell runs npm.
//
// A shell that loaded nvx's integration has NPM_CONFIG_PREFIX pointing at the
// active version's npm_global. A shell that did not (cmd.exe, an IDE task, CI,
// an agent) left npm on its own default, which is the Node.js install itself.
// So `nvx --no-sandbox npm i -g cowsay` put the tool in the version's bin
// directory in one shell and in npm_global in the other, and `npm ls -g` listed
// different tools in each. Measured 2026-10-07 on Debian, cowsay landed in
// versions/node/v22.23.3/bin with no integration and in npm_global/bin with it.

// makeNpmPrefixDir creates a global prefix the way npm expects to find it. `npm ls
// -g` fails with ENOENT for a prefix that is missing, and on Unix for one with no
// lib directory in it. Measured 2026-10-07 with npm 10.9.9 in Node.js 22.23.3 on
// Linux, an empty prefix gave exit 254 for its lib and a prefix holding an empty
// lib listed nothing and exited 0. On Windows the prefix alone is enough.
func makeNpmPrefixDir(prefixDir string) error {
	dir := prefixDir
	if runtime.GOOS != "windows" {
		dir = filepath.Join(prefixDir, "lib")
	}
	return os.MkdirAll(dir, 0700)
}

// withDefaultNpmPrefix gives an uncontained npm the prefix the integration would
// have set. It changes nothing when the person chose one, whether as
// NPM_CONFIG_PREFIX in the environment or as a prefix in their ~/.npmrc. It does
// nothing for any command but npm, or when npm is not one inside an nvx-managed
// Node.js.
//
// env is the child's environment, nil meaning it inherits this process's.
// binaryPath is the executable the shim is about to start. The version comes from
// where that file is, so nothing is resolved a second time on every npm call.
func withDefaultNpmPrefix(env []string, cmdName, nvxHome, binaryPath string, policy Policy) []string {
	if cmdName != "npm" {
		return env
	}
	versionDir := nvxNodeVersionDir(nvxHome, binaryPath)
	if versionDir == "" {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	for _, kv := range env {
		// npm reads npm_config_prefix as it reads NPM_CONFIG_PREFIX.
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "NPM_CONFIG_PREFIX") {
			return env
		}
	}
	if npmrcChoosesAPrefix() {
		return env
	}
	return append(env, "NPM_CONFIG_PREFIX="+npmPrefixDirFor(policy, versionDir))
}

// nvxNodeVersionDir returns the versions/node/<version> directory that path is
// inside, or "" for a file nvx does not manage.
func nvxNodeVersionDir(nvxHome, path string) string {
	root := filepath.Join(nvxHome, "versions", "node")
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ""
	}
	version, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if !strings.HasPrefix(version, "v") || isStagingVersionDir(version) {
		return ""
	}
	return filepath.Join(root, version)
}

// npmrcChoosesAPrefix reports a prefix set in the user's npmrc. A project's .npmrc
// is not asked. Measured 2026-10-07 with npm 10.9.9, a prefix there is refused
// ("config prefix cannot be changed from project config") and `npm prefix -g`
// ignores it, while one in the user's npmrc applies.
func npmrcChoosesAPrefix() bool {
	userrc := os.Getenv("NPM_CONFIG_USERCONFIG")
	if userrc == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userrc = filepath.Join(home, ".npmrc")
		}
	}
	return npmrcSetsPrefix(userrc)
}

// npmrcSetsPrefix reports a `prefix=` line in the file at path.
func npmrcSetsPrefix(path string) bool {
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if key, _, ok := strings.Cut(line, "="); ok && strings.EqualFold(strings.TrimSpace(key), "prefix") {
			return true
		}
	}
	return false
}
