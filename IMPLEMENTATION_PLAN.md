# Next-Gen NVX Architecture — Autonomous Implementation Plan

This document outlines the step-by-step implementation plan to modernize `nvx`'s security and runtime architecture. It addresses the core architectural pain points (Windows AppContainer named-pipe hangs, dev server loopback blocks, `.env` file leakage, and shallow supply-chain scanning) while strictly adhering to the core constraints: **zero third-party dependencies, unprivileged user-mode execution (no admin/root), and rock-solid cross-platform reliability**.

---

## User Review Required

> [!IMPORTANT]
> - **Windows Primitive Shift**: We are migrating the Windows sandbox engine from `AppContainer` to **Win32 Restricted Tokens (`CreateRestrictedToken`) combined with Job Objects (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`)**. This permanently resolves the named-pipe streaming hang and dev-server inbound loopback block while maintaining unprivileged sandbox containment.
> - **Zero Third-Party Dependencies**: All new components (streaming tarball scanner, AST capability regex/parser, APFS `clonefile` syscall, `.env` cloaking) are implemented in pure standard-library Go (`archive/tar`, `compress/gzip`, `syscall`, `os`, `net`).
> - **Fail-Closed Verification Gates**: Every milestone includes strict automated test gates that must pass before proceeding to subsequent stages.

---

## Phase Breakdown & Autonomous Execution Gates

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────────┐
│                               AUTONOMOUS EXECUTION ROADMAP & GATES                               │
├──────────────────────────────────────────────────────────────────────────────────────────────────┤
│ GATE 1: Windows Restricted Token & Job Object Engine (Fixes Dev Servers & Named Pipes)          │
│ GATE 2: In-Memory .env Secret Cloaking (Closes Credential Exfiltration Vector)                  │
│ GATE 3: Streaming Pre-Unpack Tarball Capability Scanner & 50k Typo Trie                         │
│ GATE 4: macOS Native APFS clonefile(2) Zero-Copy Staging Engine                                 │
│ GATE 5: Regression Suite, Matrix Alignment, and Documentation Updates                            │
└──────────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Proposed Changes

### Component 1: Win32 Restricted Token & Job Object Engine

Replace AppContainer process launching on Windows with unprivileged Restricted Tokens (`DISABLE_MAX_PRIVILEGE`, `LUA_TOKEN`, Restricting SIDs) coupled with Win32 Job Objects.

#### [MODIFY] [sandbox_windows.go](file:///h:/projects/private/needs-work/nvx/sandbox_windows.go)
- Refactor sandbox initialization to construct restricted security tokens via Win32 `advapi32.dll` (`CreateRestrictedToken`).
- Strip administrative SIDs and restrict the token privileges (`SE_CHANGE_NOTIFY_NAME` only).
- Attach child processes to a Win32 Job Object configured with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.

#### [MODIFY] [sandbox_appcontainer_launch_windows.go](file:///h:/projects/private/needs-work/nvx/sandbox_appcontainer_launch_windows.go)
- Transition `launchAppContainerProcessOnce` to support restricted token handles.
- Ensure native support for named pipes (`bInheritHandles = TRUE`) and standard I/O redirection.

#### [NEW] [sandbox_restricted_token_windows_test.go](file:///h:/projects/private/needs-work/nvx/sandbox_restricted_token_windows_test.go)
- Automated tests verifying:
  1. Stdio streaming on child processes (`spawn(..., {stdio: 'pipe'})`) without hangs.
  2. Localhost port binding and inbound TCP reachability (dev server test).
  3. Parent process termination triggers atomic cleanup of child process tree via Job Object.

---

### Component 2: In-Memory `.env` & Secret Cloaking Layer

Prevent package install scripts from reading `.env`, `.git/config`, or cloud credentials during untrusted execution phases.

#### [MODIFY] [sandbox_seatbelt.go](file:///h:/projects/private/needs-work/nvx/sandbox_seatbelt.go)
- Update `buildSeatbeltProfile` to inject explicit deny rules for sensitive files within `workDir`:
  ```scheme
  (deny file-read* (literal (string-append (param "WORK_DIR") "/.env")))
  (deny file-read* (literal (string-append (param "WORK_DIR") "/.env.local")))
  (deny file-read* (subpath (string-append (param "WORK_DIR") "/.git")))
  ```

#### [MODIFY] [sandbox_landlock_linux.go](file:///h:/projects/private/needs-work/nvx/sandbox_landlock_linux.go)
- In Linux mount namespace, mask `.env` and `.git/config` by mounting a zero-byte read-only memory file or excluding them from Landlock read rules.

#### [NEW] [sandbox_secret_cloaking_test.go](file:///h:/projects/private/needs-work/nvx/sandbox_secret_cloaking_test.go)
- Cross-platform test asserting that reading `.env` returns `EPERM`/`ENOENT` during `classInstall` while remaining readable in `classYourCode`.

---

### Component 3: Streaming Pre-Unpack Tarball Capability Scanner

Scan package tarball streams in memory before extracting to disk to detect zero-day malicious install hooks and embedded binaries.

#### [MODIFY] [download.go](file:///h:/projects/private/needs-work/nvx/download.go)
- Add streaming inspection hooks in `ExtractTarGz` / `ExtractZip` to analyze archive contents in-flight.

#### [MODIFY] [security.go](file:///h:/projects/private/needs-work/nvx/security.go)
- Implement `ScanTarballCapabilities(r io.Reader) (*PackageScanReport, error)`:
  - Scans `package.json` for `scripts.preinstall`, `scripts.postinstall`, `scripts.install`.
  - Parses JavaScript/TypeScript hook contents for dangerous signatures (`child_process.exec`, `fs.readFileSync('.env')`, dynamic base64 `eval()`, raw outbound socket requests).
  - Flags high-entropy binary blobs or unexpected native `.node`/`.dll`/`.exe` payloads.
- Embed a compact BK-Tree / Bloom filter representing the top 50,000 package names to replace the static 33-item array.

#### [NEW] [security_tarball_scan_test.go](file:///h:/projects/private/needs-work/nvx/security_tarball_scan_test.go)
- Table tests verifying detection of:
  1. Benign packages (clean pass).
  2. Packages attempting `.env` reads in `postinstall` (flagged/blocked).
  3. Obfuscated base64 eval scripts (flagged).
  4. Embedded executable payloads (flagged).

---

### Component 4: macOS Native APFS `clonefile(2)` Staging

Implement zero-copy directory cloning for instant runtime and guest-profile staging on macOS.

#### [MODIFY] [download.go](file:///h:/projects/private/needs-work/nvx/download.go) / [sandbox_native_darwin.go](file:///h:/projects/private/needs-work/nvx/sandbox_native_darwin.go)
- Implement `cloneDirectoryAPFS(src, dst string) error` invoking `sys_clonefile` via `syscall.Syscall` with a fallback to standard directory copy on non-APFS filesystems.

#### [NEW] [apfs_darwin_test.go](file:///h:/projects/private/needs-work/nvx/apfs_darwin_test.go)
- Unit tests asserting sub-millisecond cloning on APFS volumes.

---

### Component 5: Documentation & Enforcement Matrix Alignment

#### [MODIFY] [README.md](file:///h:/projects/private/needs-work/nvx/README.md)
- Update "Known limitations" to remove the former restrictions on dev server loopback access and named pipe streaming on Windows.
- Document `.env` cloaking guarantees during sandboxed installs.

#### [MODIFY] [docs/enforcement-matrix.md](file:///h:/projects/private/needs-work/nvx/docs/enforcement-matrix.md)
- Align the matrix with the Restricted Token architecture, updated macOS Seatbelt parameters, and streaming tarball capabilities.

---

## Verification Plan

### Automated Tests
1. **Windows Restricted Token & Pipes**:
   ```powershell
   go test -v -run "TestRestrictedToken|TestPipedStdio|TestInboundDevServerLoopback" ./...
   ```
2. **Secret Cloaking (.env)**:
   ```powershell
   go test -v -run "TestSecretCloaking" ./...
   ```
3. **Streaming Tarball Capability Scanner**:
   ```powershell
   go test -v -run "TestTarballCapability" ./...
   ```
4. **Full Test & Formatting Suite**:
   ```powershell
   gofmt -l .
   go vet ./...
   go test -race ./...
   ```

### Manual / Integration Verification
- Execute an interactive `npx vite` / `npm run dev` in a sandbox on Windows and confirm the web app loads at `http://localhost:5173`.
- Execute a synthetic test package containing a malicious `.env` reader in `postinstall` and confirm `nvx` blocks the read and flags the capability.
