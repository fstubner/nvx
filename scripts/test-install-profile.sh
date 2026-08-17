#!/bin/sh
# Regression test for install.sh's shell-profile handling.
#
# install.sh used to append only `eval "$(nvx env)"` and never put nvx on PATH, so
# every new shell printed "nvx: command not found" and nvx never activated. Nothing
# tested the installer, so it shipped that way.
#
# Run from the repo root: sh scripts/test-install-profile.sh
set -e

# Prefer the current directory when it already is the repo root, so the script
# works whether it is invoked by path, piped, or copied elsewhere.
if [ ! -f install.sh ]; then
    cd "$(dirname "$0")/.." || exit 1
fi
if [ ! -f install.sh ]; then
    echo "run this from the repo root (install.sh not found)" >&2
    exit 1
fi
fail=0

# Pull in just the profile logic, not the download half.
extract_setup() {
    # This checkout has core.autocrlf=true so the working copy carries CRLF; users
    # get LF from GitHub. Strip CR so the test exercises what they actually run.
    tr -d '\r' < install.sh \
        | sed -n '/^# 3\. Add to shell profiles/,/^case "\$SHELL_NAME" in/p' \
        | sed '$d'
}

run_case() {
    name="$1"; initial="$2"
    HOME_DIR="$(mktemp -d)"
    export HOME="$HOME_DIR"
    PROFILE="$HOME_DIR/.bashrc"
    printf '%s' "$initial" > "$PROFILE"

    # shellcheck disable=SC2086
    ( eval "$(extract_setup)"; setup_profile "$PROFILE" "true" ) >/dev/null 2>&1

    echo "=== $name ==="
    cat "$PROFILE"
    echo "--- checks ---"

    if ! grep -Fq 'export PATH="$HOME/.nvx/bin:$PATH"' "$PROFILE"; then
        echo "FAIL: PATH export missing"; fail=1
    else
        echo "ok: PATH export present"
    fi

    pathline=$(grep -Fn 'export PATH="$HOME/.nvx/bin:$PATH"' "$PROFILE" | head -1 | cut -d: -f1)
    evalline=$(grep -n 'nvx env' "$PROFILE" | head -1 | cut -d: -f1)
    if [ -n "$pathline" ] && [ -n "$evalline" ]; then
        if [ "$pathline" -lt "$evalline" ]; then
            echo "ok: PATH (line $pathline) precedes eval (line $evalline)"
        else
            echo "FAIL: PATH at $pathline does NOT precede eval at $evalline"; fail=1
        fi
    fi

    n=$(grep -Fc 'export PATH="$HOME/.nvx/bin:$PATH"' "$PROFILE" || true)
    [ "$n" = "1" ] && echo "ok: exactly one PATH line" || { echo "FAIL: $n PATH lines"; fail=1; }

    # The real test: does sourcing it put nvx on PATH?
    mkdir -p "$HOME_DIR/.nvx/bin"
    printf '#!/bin/sh\necho NVX_RAN\n' > "$HOME_DIR/.nvx/bin/nvx"
    chmod +x "$HOME_DIR/.nvx/bin/nvx"
    got=$(sh -c ". '$PROFILE' >/dev/null 2>&1; command -v nvx >/dev/null 2>&1 && nvx" 2>/dev/null || true)
    if [ "$got" = "NVX_RAN" ]; then
        echo "ok: sourcing the profile makes nvx resolvable"
    else
        echo "FAIL: nvx not resolvable after sourcing (got '$got')"; fail=1
    fi
    echo
    rm -rf "$HOME_DIR"
}

run_case "fresh profile" 'export EDITOR=vi
'

# The exact broken state the shipped installer produced.
run_case "broken existing install (eval only)" 'export EDITOR=vi

# nvx (Node Version X-platform) shell integration
eval "$(nvx env)"
'

run_case "already correct (idempotency)" 'export EDITOR=vi

# nvx (Node Version X-platform) shell integration
export PATH="$HOME/.nvx/bin:$PATH"
eval "$(nvx env)"
'

if [ "$fail" = "0" ]; then echo "ALL CHECKS PASSED"; else echo "SOME CHECKS FAILED"; exit 1; fi
