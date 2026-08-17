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

    if ! grep -Fq '.nvx/bin' "$PROFILE"; then
        echo "FAIL: PATH export missing"; fail=1
    else
        echo "ok: PATH export present"
    fi

    pathline=$(grep -Fn '.nvx/bin' "$PROFILE" | head -1 | cut -d: -f1)
    evalline=$(grep -n 'nvx env' "$PROFILE" | head -1 | cut -d: -f1)
    if [ -n "$pathline" ] && [ -n "$evalline" ]; then
        if [ "$pathline" -lt "$evalline" ]; then
            echo "ok: PATH (line $pathline) precedes eval (line $evalline)"
        else
            echo "FAIL: PATH at $pathline does NOT precede eval at $evalline"; fail=1
        fi
    fi

    n=$(grep -Fc '.nvx/bin' "$PROFILE" || true)
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

# ---------------------------------------------------------------------------
# Login-shell selection. On macOS, Terminal runs bash as a LOGIN shell, which
# reads .bash_profile/.bash_login/.profile and never .bashrc. Writing only to
# .bashrc there means nvx never activates.
# ---------------------------------------------------------------------------

run_shell_case() {
    name="$1"; setup="$2"
    HOME_DIR="$(mktemp -d)"
    export HOME="$HOME_DIR"
    export SHELL=/bin/bash
    ( cd "$HOME_DIR" && eval "$setup" )

    ( eval "$(extract_setup)"
      setup_profile "$HOME/.bashrc" "true"
      BASH_LOGIN_FILE="$(bash_login_profile)"
      if [ "$BASH_LOGIN_FILE" != "$HOME/.bashrc" ]; then
          setup_profile "$BASH_LOGIN_FILE" "true"
      fi ) >/dev/null 2>&1

    echo "=== $name ==="
    for f in .bashrc .bash_profile .bash_login .profile; do
        if [ -f "$HOME_DIR/$f" ]; then
            if grep -Fq '.nvx/bin' "$HOME_DIR/$f"; then
                echo "  $f: has nvx PATH"
            else
                echo "  $f: exists, no nvx PATH"
            fi
        else
            echo "  $f: absent"
        fi
    done
    RESULT_HOME="$HOME_DIR"
}

# macOS default: no login profile at all. One must be created, or a login shell
# gets nothing.
run_shell_case "bash, no login profile exists" 'true'
if [ -f "$RESULT_HOME/.bash_profile" ] && grep -Fq '.nvx/bin' "$RESULT_HOME/.bash_profile"; then
    echo "ok: .bash_profile created and wired"
else
    echo "FAIL: a login shell would get nothing"; fail=1
fi
rm -rf "$RESULT_HOME"; echo

# .profile exists: creating a NEW .bash_profile would make bash ignore .profile
# entirely, silently discarding the user's environment.
run_shell_case "bash, .profile exists" 'echo "export MY_SETTING=1" > .profile'
if [ -f "$RESULT_HOME/.bash_profile" ]; then
    echo "FAIL: created .bash_profile, which suppresses the existing .profile"; fail=1
else
    echo "ok: did not create .bash_profile"
fi
if grep -Fq '.nvx/bin' "$RESULT_HOME/.profile"; then
    echo "ok: wrote to .profile instead"
else
    echo "FAIL: .profile not wired"; fail=1
fi
if grep -Fq "MY_SETTING" "$RESULT_HOME/.profile"; then
    echo "ok: existing .profile content preserved"
else
    echo "FAIL: clobbered .profile"; fail=1
fi
rm -rf "$RESULT_HOME"; echo

# .bash_profile already present: use it rather than inventing another file.
run_shell_case "bash, .bash_profile exists" 'echo "export MY_SETTING=1" > .bash_profile'
if grep -Fq '.nvx/bin' "$RESULT_HOME/.bash_profile"; then
    echo "ok: used the existing .bash_profile"
else
    echo "FAIL: .bash_profile not wired"; fail=1
fi
rm -rf "$RESULT_HOME"; echo

# PATH must not accumulate when a login profile sources .bashrc (the usual
# arrangement), nor when one profile is sourced twice in a shell.
echo "=== PATH does not duplicate on repeated sourcing ==="
HOME_DIR="$(mktemp -d)"; export HOME="$HOME_DIR"
P="$HOME_DIR/.bashrc"; : > "$P"
( eval "$(extract_setup)"; setup_profile "$P" "true" ) >/dev/null 2>&1
mkdir -p "$HOME_DIR/.nvx/bin"
printf '#!/bin/sh\necho NVX_RAN\n' > "$HOME_DIR/.nvx/bin/nvx"
chmod +x "$HOME_DIR/.nvx/bin/nvx"
# \$PATH is escaped so the INNER shell expands it; unescaped, the outer shell
# substitutes its own PATH and the check silently measures nothing.
count=$(sh -c ". '$P' >/dev/null 2>&1; . '$P' >/dev/null 2>&1; printf '%s' \"\$PATH\"" | tr ':' '\n' | grep -c 'nvx/bin' || true)
if [ "$count" = "1" ]; then
    echo "ok: sourced twice, exactly one PATH entry"
else
    echo "FAIL: sourced twice, $count PATH entries"; fail=1
fi
rm -rf "$HOME_DIR"; echo

if [ "$fail" = "0" ]; then echo "ALL CHECKS PASSED"; else echo "SOME CHECKS FAILED"; exit 1; fi
