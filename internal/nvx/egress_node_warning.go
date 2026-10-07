package nvx

import (
	"path/filepath"
	"strings"
)

// Node 22 prints "[UNDICI-EHPA] Warning: EnvHttpProxyAgent is experimental" to
// stderr when a process that has NODE_USE_ENV_PROXY set exits, whether or not it
// made a request. Measured with 22.23.2, a contained `npm install` printed it
// twice and `node -e` with no network call printed it once. 24.14.1 and 24.21.0
// print nothing.
//
// --disable-warning=UNDICI-EHPA silences it, in NODE_OPTIONS so that the
// processes npm starts do too. But NODE_OPTIONS is read by every Node a contained
// process starts, and a Node that does not know the flag refuses to start:
// 18.5.0, 18.20.4 and 19.9.0 answered "--disable-warning= is not allowed in
// NODE_OPTIONS" and ran nothing. 20.11.0, 21.7.3, 22.23.2 and 24.21.0 accepted it.
// So the flag goes in only when the Node nvx resolved for the command is one that
// reads NODE_USE_ENV_PROXY, which are all releases that take it, and a command
// nvx did not resolve gets none.
const envProxyWarningFlag = "--disable-warning=UNDICI-EHPA"

// nodeReadsEnvProxy reports whether a Node of this version reads
// NODE_USE_ENV_PROXY: 24.0.0 and later, and 22.21.0 and later. See nodeUseEnvProxy.
func nodeReadsEnvProxy(v semver) bool {
	return v.major >= 24 || (v.major == 22 && v.minor >= 21)
}

// nodeVersionOf returns the version of the Node that cmdPath belongs to, when it
// is one of nvx's own, at <nvxHome>/versions/node/<version>/.... A command found
// anywhere else, and a runtime that is not Node, report false.
func nodeVersionOf(cmdPath, nvxHome string) (semver, bool) {
	rel, err := filepath.Rel(filepath.Join(nvxHome, "versions", "node"), cmdPath)
	if err != nil {
		return semver{}, false
	}
	dir, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if dir == "" || dir == "." || dir == ".." {
		return semver{}, false
	}
	v, parts, err := parseSemver(dir)
	if err != nil || parts != 3 {
		return semver{}, false
	}
	return v, true
}

// withEnvProxyWarningSilenced adds envProxyWarningFlag to NODE_OPTIONS for a
// contained command whose Node reads NODE_USE_ENV_PROXY. Anything already in
// NODE_OPTIONS stays. With no proxy the variable is not set, so there is nothing
// to warn about and the environment comes back as it was.
func withEnvProxyWarningSilenced(env []string, proxy *EgressProxy, cmdPath, nvxHome string) []string {
	if proxy == nil {
		return env
	}
	if v, ok := nodeVersionOf(cmdPath, nvxHome); !ok || !nodeReadsEnvProxy(v) {
		return env
	}
	for i, e := range env {
		name, value, found := strings.Cut(e, "=")
		if !found || !strings.EqualFold(name, "NODE_OPTIONS") {
			continue
		}
		if strings.Contains(value, envProxyWarningFlag) {
			return env
		}
		env[i] = name + "=" + strings.TrimSpace(value+" "+envProxyWarningFlag)
		return env
	}
	return append(env, "NODE_OPTIONS="+envProxyWarningFlag)
}
