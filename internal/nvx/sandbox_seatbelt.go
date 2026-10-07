package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// seatbeltExecPath is where macOS keeps sandbox-exec. A variable rather than a
// constant so a test can point it at a path that does not exist and check that
// nvx refuses to run instead of running uncontained -- the one macOS
// fail-closed claim that could not be verified while this was inlined, since
// the real file cannot be removed from a running system -- and at a stand-in
// that records its arguments, which is how the profile's location is checked.
// Shared by both launch paths, so a test can drive either.
var seatbeltExecPath = "/usr/bin/sandbox-exec"

// writeSeatbeltProfile puts the profile on disk where sandbox-exec can read
// it and contained code cannot write it, and returns the path plus a remover.
//
// Both launch paths used os.CreateTemp("", ...), which on macOS lands under
// $TMPDIR, and $TMPDIR is under /private/var/folders -- one of the roots the
// profile granted file-write* on until 2026-10-06 (see buildSeatbeltProfile). The file was 0600, but a concurrent contained process runs as
// the same user. Between nvx writing the profile and sandbox-exec reading it,
// that process could replace the contents with `(allow default)`, and the
// launch it was racing then ran with no containment at all. A process that
// watches a directory and rewrites a file is any package's postinstall
// script. Measured on the CI macOS runner: the profile's directory resolved
// to /private/var/folders/... before this change.
//
// ~/.nvx is what the profile deliberately does NOT grant writes to (see the
// comment above buildSeatbeltProfile's call sites), so it is the place.
func writeSeatbeltProfile(nvxHome, profile string) (path string, remove func(), err error) {
	if nvxHome == "" {
		nvxHome = GetHomeDir()
	}
	dir := filepath.Join(nvxHome, "seatbelt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create the Seatbelt profile directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "nvx-*.sb")
	if err != nil {
		return "", nil, fmt.Errorf("create the Seatbelt profile file: %w", err)
	}
	path = f.Name()
	remove = func() { _ = os.Remove(path) }
	if _, err := f.Write([]byte(profile)); err != nil {
		f.Close() // #nosec G104 -- the write error is what matters; a close error on top of it adds nothing
		remove()
		return "", nil, fmt.Errorf("write the Seatbelt profile: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", nil, fmt.Errorf("close the Seatbelt profile file: %w", err)
	}
	return path, remove, nil
}

// runSeatbeltSandbox wraps execution with macOS sandbox-exec (Seatbelt).
func runSeatbeltSandbox(config SandboxConfig, netCtx NetworkLaunchContext) int {
	if runtime.GOOS != "darwin" {
		LogError("The 'sandbox-exec' isolation provider is only available on macOS.")
		return 1
	}
	sandboxExec := seatbeltExecPath
	if _, err := os.Stat(sandboxExec); err != nil {
		LogError("sandbox-exec not found at %s.", sandboxExec)
		return 1
	}

	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}

	guestHome, err := createGuestProfile(config.NvxHome, sandboxID)
	if err != nil {
		LogError("Failed to create sandbox guest profile: %v", err)
		return 1
	}
	defer cleanupGuestProfile(config.NvxHome, sandboxID)

	scrubbed := scrubEnvironmentAllowing(guestHome, config.PassEnv)
	reportEnvScrub(config.NvxHome, scrubbed)
	cleanEnv := scrubbed.Env

	cwd := config.WorkDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd = containedWorkDir(config.NvxHome, guestHome, cwd)

	cmdPath, err := exec.LookPath(config.Command)
	if err != nil {
		LogError("Command not found: %s", config.Command)
		return 127
	}

	// Before the profile is rendered: the relays resolve the in-sandbox ports the
	// profile has to name.
	connectEnv, stopConnect, err := startSeatbeltConnectRelays(&netCtx)
	if err != nil {
		LogError("Could not open a path to a host service for the sandbox: %v", err)
		return 1
	}
	defer stopConnect()
	cleanEnv = append(cleanEnv, connectEnv...)

	// Only the guest home and the working directory are writable — matching
	// the Windows AppContainer and Linux Landlock write scope. nvxHome (and
	// therefore versions/*/npm_global, grants/, policy.json) and the runtime
	// binary's own directory must NOT be writable: this profile used to pass
	// both as writable roots, which let any sandboxed process rewrite the
	// global policy, self-approve grants, or trojan the node/npm binaries
	// themselves — a full, persistent sandbox defeat. Reads stay broad outside
	// the home directory and nvx's home, so the dynamic linker can find what it
	// needs. See buildSeatbeltProfile.
	profile := buildSeatbeltProfile(netCtx, guestHome, cwd, config.NvxHome, config.ReadExecRoots)
	profilePath, removeProfile, err := writeSeatbeltProfile(config.NvxHome, profile)
	if err != nil {
		LogError("Failed to write the Seatbelt profile: %v", err)
		return 1
	}
	defer removeProfile()

	args := []string{"-f", profilePath, cmdPath}
	args = append(args, config.Args...)

	cmd := exec.Command(sandboxExec, args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cwd != "" {
		cmd.Dir = cwd
	}

	LogInfo("Running in Seatbelt sandbox (session %s): %s %s", sandboxID, config.Command, strings.Join(config.Args, " "))
	// Not cmd.Run: a signalled nvx has to take the sandboxed process with it.
	if err := runChildForwardingSignals(cmd); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return childExitCode(exitErr)
		}
		LogError("Seatbelt execution failed: %v", err)
		return 1
	}
	return 0
}

// buildSeatbeltProfile renders the Seatbelt policy. The writable roots are named
// parameters rather than a variadic tail on purpose: the tail let one caller pass
// nvxHome and the runtime binary directory as writable while the other passed the
// intended two, and the compiler had no reason to object. Adding a writable root
// should now require editing this signature and every caller with it.
//
// Until 2026-10-06 every profile also granted writes on /dev, /private/tmp,
// /private/var/tmp and /private/var/folders. They came with the first Seatbelt
// profile, with no stated need. The last holds every app's per-user temp and
// cache directories, so a contained install could plant files that
// uncontained programs read later. A contained process has a temp directory
// without them, since scrubEnvironmentAllowing points TMPDIR into the guest
// home.
//
// The roots are named as given and as resolved, because Seatbelt matches the
// resolved path. A project made by mktemp is under /var/folders, really
// /private/var/folders, and a rule naming only the first would match nothing.
//
// nvxHome and readExecRoots decide what stays readable under the home
// directory. See seatbeltHomeReadRules.
func buildSeatbeltProfile(netCtx NetworkLaunchContext, guestHome, workDir, nvxHome string, readExecRoots []string) string {
	writeRoots := seatbeltPathForms(sandboxWritableRoots(guestHome, workDir))
	home, _ := os.UserHomeDir()

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process*)\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow file-read-metadata)\n")
	// Process-launch primitives. Modern macOS (especially Apple Silicon) kills a
	// process during dynamic linking if it cannot reach system Mach services or
	// map the shared cache, so a default-deny profile must permit these for any
	// binary to start. They do not weaken the filesystem-write or egress
	// containment, which are nvx's actual guarantees.
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm*)\n")
	b.WriteString("(allow iokit-open)\n")
	// Mapping a file's pages as executable is a distinct Seatbelt operation from
	// reading it; under (deny default) the linker can read but not execute its
	// libraries, so the process is killed during load. Required to run any binary.
	b.WriteString("(allow file-map-executable)\n")
	// Reads are allowed broadly. The dynamic linker must read system libraries
	// and the dyld shared cache, whose paths vary by macOS version (e.g. the
	// Cryptexes firmlink on Apple Silicon) and are impractical to enumerate
	// reliably. The home directory and nvx's own home are denied straight after,
	// with what a run needs reopened. The user's credential stores are denied
	// again further down.
	b.WriteString("(allow file-read*)\n")
	for _, rule := range seatbeltHomeReadRules(home, guestHome, workDir, nvxHome, readExecRoots) {
		b.WriteString(rule + "\n")
	}
	b.WriteString("(allow file-write*\n")
	for _, dev := range seatbeltDeviceWrites {
		b.WriteString("  " + dev + "\n")
	}
	for _, root := range dedupeStrings(writeRoots) {
		if root == "" {
			continue
		}
		fmt.Fprintf(&b, "  (subpath %q)\n", root)
	}
	b.WriteString(")\n")
	// A contained process cannot type into the terminal nvx runs on. It gets
	// that terminal as its stdin, shared with nvx, so TIOCSTI would push a byte
	// into its input queue for the user's shell to read as typed input after nvx
	// exits -- the macOS analogue of the Linux seccomp rule. file-ioctl is its
	// own Seatbelt operation, so (deny default) already refuses it (the profile
	// grants file-ioctl nowhere, and ttys here are allowed only file-read* and
	// file-write*), but that is only confirmed on recent macOS, so this denies
	// TIOCSTI by number as cheap insurance. The command is given in decimal:
	// 2147578994 is 0x80017472, TIOCSTI (_IOW('t', 114, char)). The symbol and a
	// hex literal do not parse on macOS 13 and 14. After the write allow above,
	// so the deny wins where that granted /dev/tty and /dev/ptmx.
	b.WriteString(seatbeltTerminalInputDeny + "\n")
	// The repository's git metadata stays read-only inside the writable roots;
	// see gitMetadataPaths. Seatbelt lets a later rule override an earlier one,
	// so these come after the allow. Each path is named as given and as resolved,
	// because Seatbelt matches the resolved path: a project under /var/folders is
	// really under /private/var/folders, and a deny naming only the first would
	// match nothing.
	for _, p := range seatbeltPathForms(gitMetadataPaths(workDir)) {
		fmt.Fprintf(&b, "(deny file-write* (subpath %q))\n", p)
	}
	// Dotenv files are unreadable, after the blanket read allow so the deny wins;
	// see isDotenvName.
	for _, rule := range seatbeltDotenvRules {
		b.WriteString(rule + "\n")
	}
	// The user's credential stores are unreadable, as they are on Windows and
	// Linux. These come after the blanket file-read* allow and after the reads
	// reopened under the home, so they win over both. A project or an
	// allow_read_exec root that holds a store does not expose it.
	for _, rule := range seatbeltCredentialReadDenies(home) {
		b.WriteString(rule + "\n")
	}

	// Trimmed, like every other reader of this field (policy.go, egress_proxy.go,
	// sandbox_native_windows.go, fs_provider.go). Without it a policy carrying
	// "mode": "proxy " was proxy on Windows and Linux and matched no case here, so
	// macOS silently emitted no network rule at all -- fail-closed, but a
	// platform-divergent behaviour change from one trailing space in a config file.
	//
	// Only "open" is unrestricted. An empty or unrecognised mode is proxy, as it
	// is on Windows (windowsEgressNeedsRelay). An empty mode was open here until
	// 2026-09-26. normalizePolicy kept it out of reach, and nothing here did.
	mode := strings.ToLower(strings.TrimSpace(netCtx.Mode))
	if mode == "open" {
		b.WriteString("(allow network*)\n")
	} else {
		for _, rule := range seatbeltResolverDenies {
			b.WriteString(rule + "\n")
		}
	}
	// Loopback is granted per mode, narrowly.
	//
	// Until 2026-08-20 every restricted mode emitted `(allow network-outbound
	// (remote tcp "localhost:*"))`, which let contained code reach every service on
	// the developer's machine -- their database, their other projects' dev servers
	// -- with no allowlist entry, and made `network.mode: offline` not offline. The
	// two per-port rules below it were dead: the wildcard already permitted them.
	//
	// It is the same exposure as the Windows loopback exemption, and worth being
	// precise about why it is worse than it sounds: any reachable service that
	// forwards traffic (a debugging proxy, `ssh -D`, a dev server's proxy route)
	// turns it into unrestricted egress, so the allowlist stops meaning anything.
	switch mode {
	case "open":
		// Granted in full above.
	default: // "proxy", and an empty or unrecognised mode
		// Only the proxy itself. If its ports are unknown, nothing is allowed and
		// egress fails closed rather than falling back to all of loopback.
		if netCtx.HTTPProxyPort > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", netCtx.HTTPProxyPort)
		}
		if netCtx.SOCKSProxyPort > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", netCtx.SOCKSProxyPort)
		}
	case "loopback":
		// This mode's entire meaning is "loopback is reachable", so the wildcard is
		// correct here and only here. It is not the default.
		b.WriteString("(allow network-outbound (remote tcp \"localhost:*\"))\n")
		b.WriteString("(allow network-outbound (remote udp \"localhost:*\"))\n")
		b.WriteString("(allow network-bind (local tcp \"localhost:*\"))\n")
		b.WriteString("(allow network-bind (local udp \"localhost:*\"))\n")
	case "offline":
		// Nothing. `(deny default)` above already refuses everything; emitting no
		// network rule at all is what makes offline mean offline.
	}

	// Host services named by --connect, in every mode, including offline: the
	// developer naming one has said which single service this run may reach, and
	// offline means "no network of your own", not "nothing nvx was asked to hand
	// you". The port here is nvx's own listener, never the service's own port,
	// so the sandbox reaches the relay and nvx dials the service -- see
	// sandbox_connect_darwin.go. A mapping with no resolved in-sandbox port
	// yields no rule, so a relay that failed to bind cannot widen the profile.
	for _, m := range netCtx.ConnectPorts {
		if m.Inside > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", m.Inside)
		}
	}

	return b.String()
}

// seatbeltResolverDenies keep a contained process away from mDNSResponder, the
// macOS system resolver, in every network mode but open. A lookup carries its
// name to the resolver, which sends it on to a DNS server, so a process that can
// connect nowhere could still send data out encoded in the names it asks for.
// Nothing contained needs to resolve a name: the egress proxy resolves
// allowlisted hosts in nvx, and contained clients hand it host names.
//
// mDNSResponder has two doors. getaddrinfo, which node, curl and dscacheutil
// use, connects to the socket /private/var/run/mDNSResponder, and (deny
// default) already refuses that: on the macos-latest runner the kernel logged
// node and dscacheutil denied network-outbound to it. Network.framework, which
// NSURLSession and everything built on it use, asks the Mach service
// com.apple.dnssd.service instead, and the blanket (allow mach-lookup) above
// let it through: a contained Network.framework client resolved a fresh name
// under a wildcard domain there before this rule. Seatbelt applies the last
// matching rule, so this deny, written after that allow, wins.
//
// localhost resolved inside the sandbox on the same runner with the socket
// refused, so it does not depend on either door.
// scripts/sandbox-enforcement-macos.sh checks all of this.
var seatbeltResolverDenies = []string{
	`(deny mach-lookup (global-name "com.apple.dnssd.service"))`,
}

// seatbeltTerminalInputDeny refuses ioctl TIOCSTI, which would let a contained
// process type into the terminal it shares with nvx. 2147578994 is 0x80017472,
// TIOCSTI (_IOW('t', 114, char)). Decimal on purpose: the TIOCSTI symbol and a
// hex literal both fail to parse under sandbox-exec on macOS 13 and 14.
const seatbeltTerminalInputDeny = `(deny file-ioctl (ioctl-command 2147578994))`

// seatbeltDeviceWrites are the device files a contained process may write, in
// place of all of /dev. Shell scripts write /dev/null and /dev/fd/N, which
// /dev/stdout and /dev/stderr resolve to. Programs prompt on /dev/tty, and a
// terminal session is a /dev/ttysN that a pseudo-terminal opens through
// /dev/ptmx. The dynamic linker registers DTrace probes through
// /dev/dtracehelper. The rest of /dev stays read-only, the /dev/bpf* packet
// devices among them.
var seatbeltDeviceWrites = []string{
	`(literal "/dev/null")`,
	`(literal "/dev/zero")`,
	`(literal "/dev/random")`,
	`(literal "/dev/urandom")`,
	`(literal "/dev/tty")`,
	`(literal "/dev/ptmx")`,
	`(literal "/dev/dtracehelper")`,
	`(regex #"^/dev/fd/[0-9]+$")`,
	`(regex #"^/dev/ttys[0-9]+$")`,
}

// seatbeltHomeReadRules denies reading the real home directory and nvxHome,
// then reopens what a contained run reads there. They go after the blanket
// file-read* allow and before the credential-store denies, because Seatbelt
// applies the last rule that matches.
//
// Until 2026-10-06 the profile denied only the credential stores, so a
// contained process could read every other file in the home directory, other
// projects included, and nvxHome's grants, policy and tool_home credentials.
// Windows and Linux already denied reads of the home directory. The reopened
// set is what Linux
// grants under the home (sandboxVisiblePaths): the project and the guest home,
// which are also the writable roots, every allow_read_exec root, and nvx's
// runtime trees. A runtime that lives under the home outside nvx, such as one
// installed by nvm, needs its directory in allow_read_exec, as on Linux.
//
// nvxHome is denied as well as the home, because NVX_HOME can point outside
// the home. Paths are named as given and as resolved, for the reason
// buildSeatbeltProfile gives.
func seatbeltHomeReadRules(home, guestHome, workDir, nvxHome string, readExecRoots []string) []string {
	var denied []string
	for _, p := range []string{home, nvxHome} {
		if p != "" {
			denied = append(denied, p)
		}
	}
	if len(denied) == 0 {
		return nil
	}
	denied = seatbeltPathForms(denied)

	var rules []string
	for _, p := range denied {
		rules = append(rules, fmt.Sprintf("(deny file-read* (subpath %q))", p))
	}
	// Metadata stays readable, as it is everywhere else in the profile. Node's
	// module resolution stats node_modules in each ancestor of the project,
	// and a stat shows no file contents.
	for _, p := range denied {
		rules = append(rules, fmt.Sprintf("(allow file-read-metadata (subpath %q))", p))
	}
	reopened := append(sandboxWritableRoots(guestHome, workDir), readExecRoots...)
	reopened = append(reopened, sandboxRuntimeReadRoots(nvxHome)...)
	for _, p := range seatbeltPathForms(reopened) {
		if p == "" {
			continue
		}
		rules = append(rules, fmt.Sprintf("(allow file-read* (subpath %q))", p))
	}
	return rules
}

// seatbeltDotenvRules hide dotenv files, as isDotenvName names them, wherever
// they are. Reads outside the home directory are allowed broadly on macOS, so a
// rule scoped to the working directory would leave a .env elsewhere readable,
// such as one in a monorepo root above a project in /tmp.
//
// Writes are denied as well. A process that can rename or hard-link .env can
// read the same bytes under a name the rule does not match. That also stops a
// contained tool creating or changing a .env.
//
// The templates are carved out of the rule itself. An allow after it would
// reopen them under the home directory too, where seatbeltHomeReadRules denies
// every file outside the project.
//
// Only file-read-data is denied, so the files can still be listed and
// stat'ed, as on Linux. Seatbelt applies the last rule that matches, so this
// comes after every allow it overrides.
var seatbeltDotenvRules = []string{
	`(deny file-read-data file-write* (require-all (regex #"/\.[Ee][Nn][Vv](\.[^/]*)?$") (require-not (regex #"/\.env\.(example|sample|template|dist)$"))))`,
}

// Registry tokens, keys and cloud credentials, relative to the real home.
// Files are denied as literals and directories as subpaths. pnpm keeps its rc
// under Library/Preferences on macOS and under .config elsewhere. None of these
// is on the dynamic linker's path, and the guest home a contained process gets
// as $HOME has none of them, so contained npm reads no user .npmrc either way.
var (
	credentialStoreFiles = []string{
		".npmrc", ".yarnrc", ".yarnrc.yml", ".config/pnpm/rc",
		"Library/Preferences/pnpm/rc", ".bunfig.toml", ".docker/config.json",
		".netrc", ".git-credentials",
	}
	credentialStoreDirs = []string{
		".ssh", ".aws", ".gnupg", ".config/gh", ".kube", ".config/gcloud",
		".azure", "Library/Keychains",
	}
)

// seatbeltCredentialReadDenies returns the profile rules that deny reading the
// credential stores under home. Each path is named under home as given and
// under home with symbolic links resolved, so a store that does not exist yet
// is still matched when home itself is a link. seatbeltPathForms adds the
// resolved form of a store that is itself a link. A deny on an absent path
// matches nothing and costs nothing.
func seatbeltCredentialReadDenies(home string) []string {
	if home == "" {
		return nil
	}
	homes := []string{home}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		homes = append(homes, resolved)
	}
	var rules []string
	for _, group := range []struct {
		filter string
		rels   []string
	}{{"literal", credentialStoreFiles}, {"subpath", credentialStoreDirs}} {
		var paths []string
		for _, h := range dedupeStrings(homes) {
			for _, rel := range group.rels {
				paths = append(paths, filepath.Join(h, filepath.FromSlash(rel)))
			}
		}
		for _, p := range seatbeltPathForms(paths) {
			rules = append(rules, fmt.Sprintf("(deny file-read* (%s %q))", group.filter, p))
		}
	}
	return rules
}

// seatbeltPathForms returns each path and, where it differs, the path with
// symbolic links resolved. Seatbelt matches the resolved path.
func seatbeltPathForms(paths []string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
			out = append(out, resolved)
		}
	}
	return dedupeStrings(out)
}
