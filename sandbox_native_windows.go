//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// nodeSandboxPreserveFlags let node run inside an AppContainer. Node's module
// loader realpath's the entry and every require, which lstats each path up to
// the drive root (C:\). An AppContainer cannot stat C:\ (that grant needs
// elevation), so without these flags any file/require fails with EPERM. The
// flags skip the realpath walk. Bypass with --no-sandbox if a project relies on
// symlink-resolved module identity (e.g. some pnpm layouts).
var nodeSandboxPreserveFlags = []string{"--preserve-symlinks-main", "--preserve-symlinks"}

// rewriteWindowsNodeCommand adapts a resolved command for AppContainer launch:
//   - npm.cmd / npx.cmd become a direct "node.exe <cli>.js" call, because batch
//     files can't be CreateProcess'd and the cmd.exe fallback is denied inside
//     the container.
//   - any node.exe invocation gains the preserve-symlinks flags (see above).
//
// nodeExeFallback is used when no node.exe sits beside the .cmd, which is the
// normal layout for a self-updated npm living in a version's npm_global prefix.
// Without it the rewrite would bail and launch the batch wrapper, whose own
// `IF EXIST "%dp0%\node.exe"` check then fails and degrades to a bare `node`
// that is not resolvable inside the container.
func rewriteWindowsNodeCommand(cmdPath string, args []string, nodeExeFallback string) (string, []string) {
	switch strings.ToLower(filepath.Base(cmdPath)) {
	case "npm.cmd", "npx.cmd":
		cli := "npm-cli.js"
		if strings.EqualFold(filepath.Base(cmdPath), "npx.cmd") {
			cli = "npx-cli.js"
		}
		dir := filepath.Dir(cmdPath)
		nodeExe := filepath.Join(dir, "node.exe")
		// Keep the CLI next to the .cmd we resolved, so a self-updated npm stays
		// the one that runs; only the interpreter falls back.
		cliPath := filepath.Join(dir, "node_modules", "npm", "bin", cli)
		if !regularFileExists(nodeExe) && regularFileExists(nodeExeFallback) {
			nodeExe = nodeExeFallback
		}
		if !regularFileExists(nodeExe) || !regularFileExists(cliPath) {
			return cmdPath, args
		}
		rewritten := make([]string, 0, len(nodeSandboxPreserveFlags)+1+len(args))
		rewritten = append(rewritten, nodeSandboxPreserveFlags...)
		rewritten = append(rewritten, cliPath)
		rewritten = append(rewritten, args...)
		return nodeExe, rewritten
	case "node.exe":
		rewritten := make([]string, 0, len(nodeSandboxPreserveFlags)+len(args))
		rewritten = append(rewritten, nodeSandboxPreserveFlags...)
		rewritten = append(rewritten, args...)
		return cmdPath, rewritten
	default:
		return cmdPath, args
	}
}

func regularFileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// resolveSandboxNodeExe locates the node.exe of the runtime version this session
// is using, for use as the interpreter when a resolved npm.cmd/npx.cmd has no
// node.exe of its own. Returns "" if no version is active, in which case the
// rewrite simply declines rather than guessing.
func resolveSandboxNodeExe(nvxHome string) string {
	rt := runtimeForShim("node")
	ver := getActiveShellVersionFor(nvxHome, rt.Name())
	if ver == "" {
		ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}
	if ver == "" {
		return ""
	}
	return resolvePinnedCommandPath("node", nvxHome, ver, rt)
}

// platformLaunchNative applies AppContainer isolation on Windows.
// Isolation setup is fail-closed: if AppContainer cannot be applied, the command
// is not executed.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	// One AppContainer package per project.
	//
	// This used to be one package for the whole machine, on the reasoning that
	// "isolation comes from the ephemeral guest home, capability restrictions, and
	// filesystem ACLs, not SID uniqueness". That is true of the filesystem and
	// false of the network: Windows permits loopback within a package, so every
	// port a contained process bound was reachable from every other nvx sandbox on
	// the machine. See sandbox_packages_windows.go for the measurement.
	scope := sandboxScopeForWorkDir(workDir)
	// The guest home is named after the session id, which is the only place it
	// is available here; for a trusted tool it is that tool's persistent profile
	// key, which is equally unique to one project and tool.
	pkgName := sandboxPackageName(scope, filepath.Base(guestHome))

	// Claim the package BEFORE registering it, so no profile ever exists that
	// nothing is recorded as using. Registering first would leave a window in
	// which a concurrent sweep sees an unattributed profile -- which is precisely
	// the moment it would decide the profile is orphaned.
	writeGuestHomePackage(guestHome, pkgName)
	noteSandboxPackageUse(config.NvxHome, pkgName)

	sid, err := ensureAppContainerSID(pkgName)
	if err != nil {
		LogError("AppContainer profile unavailable: %v", err)
		return 1
	}
	defer syscall.LocalFree(syscall.Handle(sid))

	// Windows redirects an AppContainer's temp to
	// <LOCALAPPDATA>\Packages\<package>\AC\Temp, and LOCALAPPDATA points into the
	// guest home. The directory has to exist or os.tmpdir() inside the sandbox is a
	// path that is not there and every mkdtemp fails with ENOENT. It was created by
	// createProfileSkeleton while the package name was a constant; now that the
	// name is known only here, it is created here.
	if err := os.MkdirAll(filepath.Join(guestHome, "AppData", "Local", "Packages", pkgName, "AC", "Temp"), 0700); err != nil {
		LogError("Could not create the sandbox temp directory: %v", err)
		return 1
	}

	// Package-manager workflows used to require the elevated `nvx setup` grants,
	// because node resolved its entry point by realpath'ing up to the drive root
	// -- a stat an AppContainer cannot do there. NODE_OPTIONS now carries
	// --preserve-symlinks into every child, so that walk no longer happens and
	// the sandbox runs unelevated. Verified against an AppContainer SID holding no
	// grant on the system drive root at all (see the unelevated probe test): both
	// `npm -v` and `npm run <script>` complete normally. So this is advisory now,
	// not a refusal -- elevation buys allowlisted egress and drive-root access for
	// tools that still walk that far, and nothing else.
	if isPackageManagerCommand(config.Command) {
		noteMissingElevatedGrants(config.NvxHome, sid, workDir)
	}

	// Withdraw stale read/execute grants BEFORE the writable roots are set up, not
	// after.
	//
	// Withdrawing is not selective -- icacls removes an identity's whole granted
	// entry on a path, not one right from it. Run the other way round, reclaiming a
	// stale entry on a directory that is also this run's working directory deleted
	// the write access just granted for it, and the command died with "chdir:
	// Access is denied". Doing it first means prepareAppContainerFilesystem
	// re-establishes whatever this run actually needs, after anything the policy no
	// longer asks for is gone.
	ledger := loadProjectGrants(config.NvxHome, scope)
	beforeCount := len(ledger.ReadExecGrants)
	var revokedNow []readExecGrant
	if capSID, cerr := scopeCapabilitySID(scope); cerr == nil {
		ledger.ReadExecGrants, revokedNow = reconcileReadExecGrants(
			ledger.ReadExecGrants, config.ReadExecRoots, []string{capSID}, revokeSandboxReadExec)
	}

	scopeCaps, err := prepareAppContainerFilesystem(sid, config.NvxHome, guestHome, workDir)
	if err != nil {
		LogError("AppContainer filesystem setup failed: %v", err)
		return 1
	}

	// Extra read/execute roots from isolation.filesystem.allow_read_exec, granted
	// to THIS PROJECT's capability rather than the shared package identity.
	//
	// The distinction matters because these ACEs persist on disk: granted to the
	// package SID, one project asking for a browser cache would admit every
	// sandbox on the machine, for ever, and removing the policy entry would not
	// take it back. Scoped to the capability, only sandboxes carrying this
	// project's identity are let in -- the same reasoning that made the writable
	// roots per-project in 0.5.0.
	//
	// Best-effort, like the working-directory grant: a failure costs the feature
	// that needed the path, not the run.
	//
	// Record the intent BEFORE granting, and refuse to grant if it cannot be
	// recorded.
	//
	// The other order left a permission on disk that nothing named: a grants file
	// that could not be written -- read-only, full disk, a permissions problem of
	// its own -- produced a warning saying nvx would not be able to withdraw the
	// grant later, and then granted it anyway. Nothing could find it afterwards,
	// including `grants reset`, because the only thing that would have named it was
	// the file that failed to save.
	//
	// This way round the failure is harmless in the other direction: a record whose
	// permission was never written costs one no-op withdrawal on some later run.
	if len(config.ReadExecRoots) > 0 && scope != "" {
		// Only what will actually be nvx's own entry. A root that already carries a
		// broader permission -- the project directory itself is both a writable root
		// and a plausible allow_read_exec entry -- is left out: nvx will not write
		// over it, so it is not nvx's to take back, and recording it meant `grants
		// reset` deleted the sandbox's write access to the user's own project while
		// reporting it had withdrawn a read/execute permission.
		intended := planReadExecRecords(ledger.ReadExecGrants, config.ReadExecRoots, scopeCaps)
		if len(intended) != beforeCount || len(revokedNow) > 0 {
			stored, _ := readGrantsFile(grantsPath(config.NvxHome, scope))
			ledger.ReadExecGrants = mergeLedgerForSave(intended, stored, revokedNow)
			ledger.ProjectPath = scope
			if err := saveProjectGrants(config.NvxHome, ledger); err != nil {
				LogError("Could not record the read/execute permissions this policy asks for: %v", err)
				LogInfo("They were not granted: a permission nvx cannot record is one it could never withdraw.")
				return 1
			}
		}
	} else if scope != "" && len(revokedNow) > 0 {
		// Withdrawals still need writing back even with nothing left to grant.
		stored, _ := readGrantsFile(grantsPath(config.NvxHome, scope))
		ledger.ReadExecGrants = mergeLedgerForSave(ledger.ReadExecGrants, stored, revokedNow)
		ledger.ProjectPath = scope
		if err := saveProjectGrants(config.NvxHome, ledger); err != nil {
			LogWarn("Could not update the record of the sandbox's read grants: %v", err)
		}
	}

	for _, root := range config.ReadExecRoots {
		granted := false
		for _, capSID := range scopeCaps {
			if _, err := grantSandboxReadExec(capSID, root); err != nil {
				// The record already names this path, which is the safe direction: a
				// record with no permission behind it is withdrawn as a no-op, while a
				// permission with no record is invisible.
				LogWarn("Could not grant the sandbox read access to %s: %v", root, err)
				continue
			}
			granted = true
		}
		if granted {
			LogInfo("Sandbox may read and execute from %s", root)
		}
	}
	// Make the command reachable from inside the container BEFORE rewriting it.
	//
	// A runtime outside ~/.nvx/versions is copied into nvxHome, because its own
	// location may not be grantable. The rewrite below then derives npm-cli.js from
	// the directory it is handed, so it has to be handed the copy: run the other way
	// round, it produced a launch whose interpreter was the staged node.exe but
	// whose script argument still pointed into the original directory -- which the
	// container has no grant on, so node failed with "Cannot find module
	// C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js".
	cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
	if err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}
	grantedDir := filepath.Dir(cmdPath)

	// Adapt node/npm/npx for AppContainer launch (direct node.exe, realpath-safe).
	cmdPath, launchArgs := rewriteWindowsNodeCommand(cmdPath, config.Args, resolveSandboxNodeExe(config.NvxHome))

	// When the resolved npm.cmd has no sibling node.exe -- the normal layout for a
	// self-updated npm in a version's npm_global prefix -- the rewrite falls back to
	// the active runtime's interpreter, which sits outside the directory just
	// granted and needs one of its own.
	if !strings.EqualFold(filepath.Dir(cmdPath), grantedDir) {
		cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
		if err != nil {
			LogError("AppContainer executable access failed: %v", err)
			return 1
		}
	}

	// Put the resolved runtime's directory on PATH so tools spawned inside the
	// sandbox (e.g. an npx-installed CLI whose launcher calls `node`) can find it.
	// The host's own node dir is on PATH but is not accessible to the container;
	// this directory is granted RX above.
	cleanEnv = prependPath(cleanEnv, filepath.Dir(cmdPath))

	// The preserve-symlinks flags above only cover the process nvx launches.
	// npm scripts spawn further node processes, whose own entry-point resolution
	// realpaths up to the drive root — a path an AppContainer cannot stat unless
	// that volume's root was granted by `nvx setup` (only the system drive is,
	// so any project on another drive fails). NODE_OPTIONS carries the flags into
	// every child so the realpath walk is skipped there too.
	cleanEnv = setNodeOptionsPreserveSymlinks(cleanEnv)

	// Lifecycle scripts must inherit stdio rather than have it piped, or they
	// hang forever. See forceForegroundScripts.
	cleanEnv = forceForegroundScripts(cleanEnv)

	// That covers npm piping a script's output. It does not cover a script that
	// captures its OWN child, which is a different process creating a different
	// pipe -- esbuild's postinstall, and the reason `npm install esbuild` hung.
	// The preload makes the synchronous capture APIs use file descriptors instead.
	if shim, err := writeStdioShim(guestHome); err != nil {
		LogWarn("Could not install the stdio compatibility preload: %v", err)
		LogInfo("An install script that captures a subprocess's output may hang; run that install with --no-sandbox.")
	} else {
		cleanEnv = addNodeOptionsRequire(cleanEnv, shim)
	}

	// Streaming capture needs a stream, which the preload's temp files cannot
	// be. Contained code cannot create a named pipe, so nvx creates a small pool
	// out here and the preload only opens them -- see sandbox_stdio_broker_windows.go.
	//
	// Silent when it does not work: the fallback is the behaviour that shipped
	// before, so there is nothing for a user to act on, and this runs on every
	// contained launch.
	if sidStr, err := appContainerSidToString(sid); err == nil {
		// The guest home is named after the sandbox id, which is what makes the
		// pipe names unique between concurrent sessions.
		broker, channelNames := provisionStdioChannels(sidStr, stdioSessionID(filepath.Base(guestHome)))
		if broker != nil {
			defer broker.Close()
			cleanEnv = addStdioChannelsEnv(cleanEnv, channelNames)
		}
	}

	// Network. In proxy mode (the default) the container is granted NO network
	// capability at all, and reaches the parent's egress proxy through an in-
	// container relay -- so the allowlist is enforced by the OS rather than
	// merely advertised in HTTP_PROXY. offline/loopback also grant nothing and get
	// no relay. Only network.mode "open" grants internetClient and connects direct.
	networkCaps, useRelay := windowsSandboxNetwork(netCtx.Mode)
	if !useRelay {
		cleanEnv = stripProxyEnv(cleanEnv)
	}
	// A leftover exemption from a pre-0.5.0 elevated setup makes every loopback
	// service on the machine reachable regardless of the allowlist, and nvx cannot
	// remove it unelevated. Say so rather than let the allowlist look enforced.
	if sidStr, err := appContainerSidToString(sid); err == nil {
		warnIfSandboxLoopbackExempt(config.NvxHome, sidStr, netCtx.Mode)
	}
	// The project capability is what makes this session's writable roots reachable
	// at all; without it the container holds the package SID only, which no longer
	// grants the guest home or the working directory.
	capabilitySIDs := launchCapabilitySIDs(scopeCaps, networkCaps)

	// Publishing a port needs the in-container supervisor too, since the tunnels
	// are dialled from in there. In the default proxy mode it is already running
	// for the relay; this is what makes --expose work in "open" mode as well,
	// where nothing else would have started it.
	exposeCtx, cancelExpose := context.WithCancel(context.Background())
	defer cancelExpose()
	for _, m := range netCtx.ExposePorts {
		e, perr := publishExposedPort(exposeCtx, guestHome, m)
		if perr != nil {
			LogError("Could not publish port %d from the sandbox: %v", m.Container, perr)
			return 1
		}
		defer e.Close()
		// The host port is the one the developer types, and it is deliberately not
		// the one their dev server prints, so say both.
		LogInfo("Sandbox port %d is published at http://127.0.0.1:%d (the URL the server prints is only valid inside)",
			m.Container, e.hostPort)
	}

	// Host services this sandbox may reach. The parent opens the socket and picks
	// the in-sandbox port, so the supervisor is told both numbers rather than
	// deciding either -- the contained side never chooses where it can dial.
	for i, m := range netCtx.ConnectPorts {
		c, cerr := openConnectPort(exposeCtx, config.NvxHome, guestHome, m)
		if cerr != nil {
			LogError("Could not open a path to 127.0.0.1:%d for the sandbox: %v", m.Host, cerr)
			return 1
		}
		defer c.Close()
		if netCtx.ConnectPorts[i].Inside == 0 {
			netCtx.ConnectPorts[i].Inside = freeLoopbackPort()
		}
		LogWarn("The sandbox may reach 127.0.0.1:%d on this machine, as 127.0.0.1:%d inside it (%s).",
			m.Host, netCtx.ConnectPorts[i].Inside, connectEnvVar(m.Host))
		cleanEnv = append(cleanEnv, fmt.Sprintf("%s=%d", connectEnvVar(m.Host), netCtx.ConnectPorts[i].Inside))
	}

	if useRelay || len(netCtx.ExposePorts) > 0 || len(netCtx.ConnectPorts) > 0 {
		cmdPath, launchArgs, err = wrapWithEgressSupervisor(
			sid, config.NvxHome, guestHome, workDir, netCtx, cmdPath, launchArgs,
		)
		if err != nil {
			// Fail closed: falling back to a direct connection would silently
			// restore the unrestricted egress this whole path exists to remove.
			LogError("Egress relay setup failed (fail-closed): %v", err)
			LogInfo("To run without the egress allowlist, set network.mode to \"open\" in your nvx policy.")
			return 1
		}
	}

	LogInfo("Windows AppContainer isolation active")
	// An install that trips the named-pipe restriction blocks forever and prints
	// nothing; this turns that silence into a diagnosis. See sandbox_hang_hint.
	stopHint := startHangHint(config.Command, config.Args)
	defer stopHint()
	exitCode, err := launchAppContainerProcess(
		cmdPath, launchArgs, cleanEnv, workDir, sid, 0, capabilitySIDs,
	)
	// A staged supervisor can be corrupted in place without changing its size --
	// an antivirus quarantine stub, a cloud-sync placeholder, a bad sector. The
	// reuse check compares size, so it would never notice, and every later
	// contained launch would fail the same way with nothing able to clear it.
	// Discard it and try once more; a second failure is reported as it stands.
	if err != nil && useRelay && stagedImageIsUnusable(err) {
		LogWarn("The staged sandbox supervisor is unusable; replacing it and retrying once.")
		if rerr := restageSupervisor(config.NvxHome, cmdPath); rerr != nil {
			LogError("Could not replace the sandbox supervisor: %v", rerr)
			return 1
		}
		exitCode, err = launchAppContainerProcess(
			cmdPath, launchArgs, cleanEnv, workDir, sid, 0, capabilitySIDs,
		)
	}
	if err != nil {
		// A launch failure is the one signal that a remembered grant may have gone
		// stale -- an ACE removed behind nvx's back by a repair tool or a profile
		// reset. Forget them so the next run re-reads every ACL and re-grants what
		// is missing, rather than skipping the fix for a week and leaving the cure
		// to someone who knows the cache file exists.
		invalidateGrantCache()
		LogError("AppContainer launch failed: %v", err)
		return 1
	}
	// Say it again, last, when the command failed and a stranded setup is why it
	// probably failed.
	//
	// The advisory is printed before the child runs, which is the right place for
	// it -- and then npm prints twenty-odd lines of its own on the way out, ending
	// with advice to check your antivirus and re-run "as root/Administrator".
	// Running elevated is not the fix; `nvx setup` is, once. The commit that added
	// the advisory was titled "instead of letting npm say EPERM", and what shipped
	// was a prefix: an acceptance pass pointed out that the last twenty-five lines
	// a person reads are still npm's, and wrong.
	if exitCode != 0 && isPackageManagerCommand(config.Command) {
		remindAboutStrandedSetup(config.NvxHome)
	}
	return exitCode
}

// remindAboutStrandedSetup repeats the stranded-grant advice after a failed
// package-manager run, so the correct remedy is the last thing on screen.
//
// Only when the condition actually holds, and only after a failure: a warning
// printed after every successful install would be noise, and this one has to stay
// worth reading.
func remindAboutStrandedSetup(nvxHome string) {
	current, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		return
	}
	prev, ok := readWindowsSetupState(nvxHome)
	if !ok || strings.EqualFold(prev.AppContainerSID, current) {
		return
	}
	LogWarn("That command failed, and an earlier 'nvx setup' no longer applies to this sandbox.")
	LogInfo("If the error above mentions EPERM on a path like C:\\Users, the fix is 'nvx setup' from an Administrator terminal -- not re-running this as Administrator.")
}

// windowsSandboxNetwork decides the AppContainer's network capabilities and
// whether the launch goes through the in-container egress relay.
//
// Until 0.5.0 the default granted internetClient and connected directly, because
// an AppContainer cannot reach a loopback listener outside itself without an
// elevated exemption -- so the egress allowlist was cooperative, and a package
// that ignored HTTP_PROXY reached anything it wanted. The relay removes that: with
// no capability granted, direct connections are refused by the OS and DNS does not
// resolve, and the only route out is the parent's proxy.
func windowsSandboxNetwork(mode string) (capabilitySIDs []string, useRelay bool) {
	if windowsEgressNeedsRelay(mode) {
		return nil, true
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "offline", "loopback":
		return nil, false // no capabilities: the sandbox has no network at all
	}
	return []string{capabilityInternetClientSID}, false // "open", by request
}

// wrapWithEgressSupervisor rewrites the launch to run nvx's in-container
// supervisor, which hosts the egress relay and then spawns the real target. It
// returns the supervisor's path and argument list.
func wrapWithEgressSupervisor(
	sid uintptr, nvxHome, guestHome, workDir string,
	netCtx NetworkLaunchContext, cmdPath string, args []string,
) (string, []string, error) {
	// The egress socket is required only when the relay is the reason we are here.
	// A launch that wraps solely to publish a port (network.mode "open") has no
	// proxy to reach and must not be refused for lacking one.
	if netCtx.EgressSocketPath == "" && windowsEgressNeedsRelay(netCtx.Mode) {
		return "", nil, fmt.Errorf("no egress socket was prepared for this session")
	}
	supervisor, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		return "", nil, err
	}
	if err := grantAppContainerPathReadExecTree(sid, filepath.Dir(supervisor)); err != nil {
		return "", nil, fmt.Errorf("grant the supervisor to the sandbox: %w", err)
	}
	_, _ = grantWorkdirAncestors(sid, nvxHome, filepath.Dir(supervisor))

	supervisorArgs := []string{
		"__appcontainer-exec",
		"--guest-home=" + guestHome,
		"--work-dir=" + workDir,
		"--nvx-home=" + nvxHome,
		"--network-mode=" + netCtx.Mode,
		"--egress-socket=" + netCtx.EgressSocketPath,
	}
	// Only the container port crosses: inside the sandbox the host mapping is
	// meaningless, and the tunnel socket is named by the container port.
	for _, m := range netCtx.ExposePorts {
		supervisorArgs = append(supervisorArgs, "--expose="+strconv.Itoa(m.Container))
	}
	for _, m := range netCtx.ConnectPorts {
		supervisorArgs = append(supervisorArgs,
			fmt.Sprintf("--connect=%d:%d", m.Host, m.Inside))
	}
	supervisorArgs = append(supervisorArgs, "--", cmdPath)
	return supervisor, append(supervisorArgs, args...), nil
}

// prependPath puts dir at the front of the PATH entry in env (case-insensitive
// key match), adding a PATH entry if none exists.
func prependPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	for i, e := range env {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "PATH") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + kv[1]
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// noteMissingElevatedGrants reports which host paths the sandbox cannot read,
// based on the actual ACLs rather than on whether a setup marker file exists --
// so it stays accurate if setup was undone, or if a project sits on a volume a
// previous setup did not cover. Only an elevated `nvx setup` can add them.
//
// Checked against the identity `nvx setup` GRANTS, not against this run's
// package. Those were the same thing while one package served the whole machine.
// They stopped being the same when packages became per project, and the gap is
// not cosmetic: on a machine set up before that change the grant sits on a
// package nothing launches under any more, so `npx` fails with a raw
// "EPERM: operation not permitted, lstat" out of npm's own dependency walker and
// nvx says nothing at all. Measured 2026-08-30 -- the same command succeeds on
// 0.5.7 and fails on this build, on an account whose windows-setup.json records
// the older package SID.
//
// Every path setup grants is checked, not only drive roots. Setup grants the
// system drive root and the profile parent, and it is the profile parent that
// npm's walk actually trips over; checking only the root reported nothing while
// the failing path was unreadable.
//
// The "already told you" marker is keyed by identity as well as path, so an
// upgrade that changes which identity needs the grant re-arms the notice rather
// than inheriting a tick from the old one.
func noteMissingElevatedGrants(nvxHome string, sid uintptr, workDir string) {
	sidStr, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		return
	}

	// The roots THIS run could actually walk to, which is the same set `nvx setup`
	// would grant if run from here -- so the notice cannot name a path the
	// suggested command would not fix, and cannot list one twice.
	//
	// It used to take every fixed volume on the machine and then append the working
	// directory's, which named volumes no project was on and printed the current
	// one twice ("... or G:\ or H:\ or H:\"). Worse, it made the notice look like a
	// complete account of why a contained command had failed, when the failure
	// measured on 2026-09-01 was inside nvx's own home and no amount of elevated
	// setup would have touched it.
	roots, _ := windowsSetupGrantPaths(nvxHome, workDir, false)

	// A machine that ran setup before per-project packages has the grant on an
	// identity nothing carries now. That case is not advisory -- it BREAKS `npx`,
	// today, with an error from npm that says nothing about nvx -- so it is not
	// suppressed after one showing.
	//
	// It was. The marker is keyed to a machine-wide identity, so the first run
	// anywhere consumed it and every project afterwards got npm's raw EPERM in
	// silence; an acceptance pass found a brand-new project getting no explanation
	// at all, in the commit titled "Say why npx fails after an upgrade". Once per
	// machine is right for "you never ran setup and probably do not need to" and
	// wrong for "the thing you are running is about to fail until you act".
	prev, hadSetup := readWindowsSetupState(nvxHome)
	stranded := hadSetup && !strings.EqualFold(prev.AppContainerSID, sidStr)

	var missing []string
	for _, r := range roots {
		if appContainerHasGrant(sidStr, r) {
			continue
		}
		if !stranded && driveRootNoticeSeen(nvxHome, sidStr, r) {
			continue
		}
		missing = append(missing, r)
	}
	if len(missing) == 0 {
		return
	}

	if stranded {
		LogWarn("An earlier 'nvx setup' granted %s to a sandbox identity nvx no longer uses, so that grant no longer applies.", strings.Join(missing, " or "))
		// "may fail", not "fails". This notice named the only cause it knew about,
		// and a reader took it as the explanation for an EPERM that was coming from
		// somewhere else entirely -- and then spent the better part of an hour on an
		// elevated command that could not have fixed it.
		LogInfo("Re-run 'nvx setup' from an Administrator terminal, in this directory, to move it. " +
			"Until then a tool that walks up to one of those paths may fail there with EPERM. " +
			"An EPERM on a path inside nvx's own home is a different problem and needs no elevation.")
		// Deliberately not marked as seen: it repeats until setup is re-run, which
		// is what clears the condition.
		return
	}

	LogWarn("The sandbox cannot read %s. Most workflows are unaffected.", strings.Join(missing, " or "))
	LogInfo("A tool that resolves paths that far may fail there. To grant it: 'nvx setup' from an Administrator terminal, run in this directory so it covers this volume.")
	for _, r := range missing {
		markDriveRootNoticeSeen(nvxHome, sidStr, r)
	}
}

func driveRootNoticeFile(nvxHome string) string {
	return filepath.Join(nvxHome, "drive-root-notices.json")
}

// driveRootNoticeSeen reports whether the advisory for root has already been
// shown. Best-effort: an unreadable/corrupt file just means the notice repeats,
// which is strictly better than suppressing it wrongly.
func driveRootNoticeSeen(nvxHome, sidStr, root string) bool {
	data, err := os.ReadFile(driveRootNoticeFile(nvxHome))
	if err != nil {
		return false
	}
	var seen []string
	if json.Unmarshal(data, &seen) != nil {
		return false
	}
	for _, s := range seen {
		if strings.EqualFold(s, sidStr+"|"+root) {
			return true
		}
	}
	return false
}

func markDriveRootNoticeSeen(nvxHome, sidStr, root string) {
	var seen []string
	if data, err := os.ReadFile(driveRootNoticeFile(nvxHome)); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	for _, s := range seen {
		if strings.EqualFold(s, sidStr+"|"+root) {
			return
		}
	}
	seen = append(seen, sidStr+"|"+root)
	data, err := json.Marshal(seen)
	if err != nil {
		return
	}
	if os.MkdirAll(nvxHome, 0o700) == nil {
		_ = os.WriteFile(driveRootNoticeFile(nvxHome), data, 0o600)
	}
}

// setNodeOptionsPreserveSymlinks ensures NODE_OPTIONS carries the
// preserve-symlinks flags, so node processes spawned *inside* the sandbox (npm
// scripts, tool launchers) skip the realpath walk that the flags on nvx's own
// launch command line only cover for the top-level process.
func setNodeOptionsPreserveSymlinks(env []string) []string {
	flags := strings.Join(nodeSandboxPreserveFlags, " ")
	for i, e := range env {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "NODE_OPTIONS") {
			if strings.Contains(kv[1], "--preserve-symlinks") {
				return env // already covered; don't duplicate
			}
			env[i] = "NODE_OPTIONS=" + strings.TrimSpace(kv[1]+" "+flags)
			return env
		}
	}
	return append(env, "NODE_OPTIONS="+flags)
}

// forceForegroundScripts makes npm run lifecycle scripts with inherited stdio
// instead of piping them.
//
// A process inside an AppContainer cannot create a named pipe -- `net.createServer`
// on a pipe name returns EACCES -- and libuv implements piped child stdio on
// Windows with exactly that. So `spawn(..., {stdio: 'pipe'})` blocks inside libuv
// before the child is ever created: the target's own timeout never fires, because
// the event loop never gets back a turn.
//
// npm pipes lifecycle-script output by default, so every `npm install` of a
// package carrying a postinstall hung indefinitely -- the whole class of package
// this sandbox exists to contain. Measured inside a real container: stdio
// 'inherit' returns in 404ms and 'ignore' in 696ms, while 'pipe' never returns.
//
// This is a workaround at the npm layer, not a fix for the restriction. Anything
// else inside the sandbox that pipes a child -- an npx tool shelling out, a script
// calling execSync with default options -- still hangs, and that is recorded as a
// known limitation rather than papered over. The npm path is fixed here because it
// is the primary one and npm hands us the switch.
//
// The visible effect is that script output goes to the terminal as it happens
// instead of being buffered by npm. For a tool whose job is to show you what a
// package does during install, that is not a regression.
func forceForegroundScripts(env []string) []string {
	const key = "npm_config_foreground_scripts"
	for _, e := range env {
		if strings.HasPrefix(strings.ToLower(e), key+"=") {
			return env // an explicit setting wins; don't override the caller
		}
	}
	return append(env, key+"=true")
}

func stripProxyEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		switch strings.ToUpper(strings.SplitN(e, "=", 2)[0]) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, e)
	}
	return out
}

// launchCapabilitySIDs is the capability set every contained launch carries.
//
// Extracted so it can be asserted. The setup capability appended here is the
// identity `nvx setup` writes drive-root ACEs for, and the two are only useful
// together: if this stops appending it, an elevated setup grants access to
// something no launch holds, contained `npx` keeps failing, and the machine's
// owner has done exactly what they were told with nothing to show for it. That
// failure is invisible from both sides -- setup reports success because the ACE
// was written, and the launch reports EPERM because it does not hold the
// identity.
//
// A machine that never ran setup simply carries a capability nothing has granted
// anything to, which costs nothing and grants nothing.
func launchCapabilitySIDs(scopeCaps, networkCaps []string) []string {
	caps := append(append([]string{}, scopeCaps...), networkCaps...)
	if setupCap, err := deriveCapabilitySIDString(setupCapabilityName); err == nil {
		caps = append(caps, setupCap)
	}
	return caps
}
