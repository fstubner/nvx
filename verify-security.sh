#!/bin/bash
# verify-security.sh
# Runs the local security and vulnerability scans on Unix/macOS/Linux.
#
# This script used to run under `set -e`, so the first tool exiting non-zero
# aborted it before the remaining checks ran and before it could report anything.
# gosec exits 1 whenever it finds an issue, so in practice the script always died
# at that step -- the mirror image of the .ps1, which always claimed success.
# Each check now runs to completion, records its exit code, and the script fails at
# the end if any did.

set -uo pipefail

# gosec exclusions, each with a reason. Anything not listed is expected to be clean,
# so a new finding fails the run instead of joining a backlog.
#   G204 - subprocess launched with a variable. nvx exists to run user-named
#          binaries; this fires on essentially every launch path.
#   G304 - file inclusion via variable. Same: paths come from policy and CLI args.
#   G301/G306 - directory and file permissions, already chosen deliberately.
#   G103 - use of unsafe. Unavoidable: the Windows sandbox calls Win32 APIs
#          directly, which requires unsafe.Pointer.
# Narrower suppressions live at the source as #nosec comments with a reason.
# Tool versions are PINNED, not @latest. A gate whose strictness drifts with
# upstream releases turns red without a code change, and then gets ignored.
# Upgrading is a deliberate act: newer gosec adds rules (the v2.2x taint-analysis
# set, G702/G703/G704) that report findings this codebase has never triaged.
GOSEC_EXCLUDE='G204,G304,G301,G306,G103'

FAILURES=""

# Where `go install` actually puts binaries. Both scripts used to hardcode
# $HOME/go/bin, which is wrong wherever GOPATH is set elsewhere -- including the
# official golang container images (GOPATH=/go) and many CI runners, where the
# tools were installed successfully and then invoked at a path that did not exist,
# giving exit 127.
GO_BIN_DIR="$(go env GOBIN 2>/dev/null || true)"
if [ -z "$GO_BIN_DIR" ]; then
    GO_BIN_DIR="$(go env GOPATH 2>/dev/null)/bin"
fi

run_check() {
    name="$1"; shift
    printf '\n\033[36m%s...\033[0m\n' "$name"
    if "$@"; then
        printf '\033[32m  %s passed\033[0m\n' "$name"
    else
        code=$?
        printf '\033[31m  %s FAILED (exit %s)\033[0m\n' "$name" "$code"
        FAILURES="$FAILURES $name"
    fi
}

echo -e "\033[36mRunning local security checks for nvx...\033[0m"

# 1. govulncheck
if command -v govulncheck >/dev/null 2>&1; then
    GOVULNCHECK="govulncheck"
elif [ -x "$GO_BIN_DIR/govulncheck" ]; then
    GOVULNCHECK="$GO_BIN_DIR/govulncheck"
else
    echo -e "\033[33mInstalling govulncheck...\033[0m"
    go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
    GOVULNCHECK="$GO_BIN_DIR/govulncheck"
fi

# 2. gosec
if command -v gosec >/dev/null 2>&1; then
    GOSEC="gosec"
elif [ -x "$GO_BIN_DIR/gosec" ]; then
    GOSEC="$GO_BIN_DIR/gosec"
else
    echo -e "\033[33mInstalling gosec...\033[0m"
    go install github.com/securego/gosec/v2/cmd/gosec@v2.28.0
    GOSEC="$GO_BIN_DIR/gosec"
fi

run_check "govulncheck" "$GOVULNCHECK" ./...
run_check "gosec" "$GOSEC" "-exclude=$GOSEC_EXCLUDE" ./...
run_check "go vet" go vet ./...

echo ""
if [ -n "$FAILURES" ]; then
    printf '\033[31mSECURITY CHECKS FAILED:%s\033[0m\n' "$FAILURES"
    printf '\033[33mgovulncheck reports against the Go toolchain in use; if it is the only failure, check `go version` before assuming the code is at fault.\033[0m\n'
    exit 1
fi

echo -e "\033[32mAll security scans passed.\033[0m"
exit 0
