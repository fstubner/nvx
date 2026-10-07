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

# fish gets its own conf.d file, not the POSIX lines in ~/.profile that it
# cannot run. Written once, in fish syntax, honouring XDG_CONFIG_HOME.
echo "=== fish ==="
# CI runners set XDG_CONFIG_HOME, which would move the file out of the scratch HOME.
unset XDG_CONFIG_HOME
HOME_DIR="$(mktemp -d)"; export HOME="$HOME_DIR"
( eval "$(extract_setup)"; setup_fish; setup_fish ) >/dev/null 2>&1
F="$HOME_DIR/.config/fish/conf.d/nvx.fish"
if [ -f "$F" ] && grep -Fq 'nvx env --shell=fish | source' "$F"; then
    echo "ok: conf.d/nvx.fish holds the fish integration"
else
    echo "FAIL: no fish integration at $F"; fail=1
fi
if [ "$(grep -Fc 'nvx env' "$F" 2>/dev/null || true)" = "1" ]; then
    echo "ok: written once across two runs"
else
    echo "FAIL: written more than once"; fail=1
fi
if [ -e "$HOME_DIR/.profile" ] || grep -Fq 'export PATH' "$F" 2>/dev/null; then
    echo "FAIL: POSIX syntax reached fish or ~/.profile"; fail=1
else
    echo "ok: no POSIX syntax written"
fi
XDG="$(mktemp -d)"
( export XDG_CONFIG_HOME="$XDG"; eval "$(extract_setup)"; setup_fish ) >/dev/null 2>&1
if [ -f "$XDG/fish/conf.d/nvx.fish" ]; then
    echo "ok: XDG_CONFIG_HOME is honoured"
else
    echo "FAIL: XDG_CONFIG_HOME ignored"; fail=1
fi
if command -v fish >/dev/null 2>&1; then
    fish --no-execute "$F" && echo "ok: fish parses the file" || { echo "FAIL: fish rejects the file"; fail=1; }
else
    echo "skip: fish is not installed here, so the file was not parsed by fish"
fi
rm -rf "$HOME_DIR" "$XDG"; echo

# ---------------------------------------------------------------------------
# A login sh has to be able to read what was written.
#
# ~/.profile is read by every login sh, and on Debian and Ubuntu that is dash.
# `nvx env` prints bash syntax for any shell that is not zsh or fish, and the
# profile ran it without a guard, so dash stopped at ${PATH//...} with "Bad
# substitution" and `sh -l` aborted for as long as nvx was installed. The stub
# nvx below prints a line of the same kind.
# ---------------------------------------------------------------------------

DASH="$(command -v dash || true)"
BASH_BIN="$(command -v bash || true)"
ZSH_BIN="$(command -v zsh || true)"

make_nvx_stub() {
    mkdir -p "$1/.nvx/bin"
    cat > "$1/.nvx/bin/nvx" <<'STUB'
#!/bin/sh
if [ "$1" = "env" ]; then
    printf '%s\n' 'PATH="${PATH//:nvx-test-absent:/:}"' 'export NVX_ENV_EVALUATED=1'
else
    echo NVX_RAN
fi
STUB
    chmod +x "$1/.nvx/bin/nvx"
}

# try_shell <shell> <profile>: the shell starts in an empty environment, sources
# the profile, and says where nvx is and whether the integration ran. Sets OUT and
# RC. The shell's own path is $0 inside the script and the profile is $1.
try_shell() {
    RC=0
    OUT=$(env -i HOME="$HOME" PATH=/usr/bin:/bin "$1" -c '. "$1"; echo "nvx=$(command -v nvx)"; echo "EVAL=${NVX_ENV_EVALUATED:-no}"' "$1" "$2" 2>&1) || RC=$?
}

# no_shell <name>: a shell this machine lacks is a failure in CI on Linux, where
# dash is the login sh that broke, and a note elsewhere. A login sh that was
# never tried proves nothing.
no_shell() {
    if [ -n "${CI:-}" ] && [ "$(uname -s)" = "Linux" ]; then
        echo "FAIL: $1 is not installed, so it did not read the profile"; fail=1
    else
        echo "skip: $1 is not installed here, so it did not read the profile"
    fi
}

# write_profile <name> <profile before the installer runs>: a fresh home whose
# ~/.profile has been through setup_profile. Leaves HOME_DIR and PROFILE set.
write_profile() {
    HOME_DIR="$(mktemp -d)"
    export HOME="$HOME_DIR"
    PROFILE="$HOME_DIR/.profile"
    printf '%s' "$2" > "$PROFILE"
    ( eval "$(extract_setup)"; setup_profile "$PROFILE" "true" ) >/dev/null 2>&1
    make_nvx_stub "$HOME_DIR"
    echo "=== $1 ==="
    cat "$PROFILE"
    echo "--- checks ---"
}

# login_case <name> <profile before the installer runs>: write_profile, then each
# shell reads the result.
login_case() {
    write_profile "$1" "$2"

    if [ -n "$DASH" ]; then
        try_shell "$DASH" "$PROFILE"
        if [ "$RC" = "0" ] && printf '%s' "$OUT" | grep -Fq "nvx=$HOME_DIR/.nvx/bin/nvx"; then
            echo "ok: a login sh (dash) reads the profile and finds nvx"
        else
            echo "FAIL: dash could not read the profile (rc=$RC): $OUT"; fail=1
        fi
        if printf '%s' "$OUT" | grep -Fq "EVAL=no"; then
            echo "ok: dash does not run the bash-only integration"
        else
            echo "FAIL: dash ran the integration: $OUT"; fail=1
        fi
    else
        no_shell dash
    fi

    if [ -n "$BASH_BIN" ]; then
        try_shell "$BASH_BIN" "$PROFILE"
        if [ "$RC" = "0" ] && printf '%s' "$OUT" | grep -Fq "EVAL=1"; then
            echo "ok: bash still loads the integration"
        else
            echo "FAIL: bash did not load the integration (rc=$RC): $OUT"; fail=1
        fi
    else
        no_shell bash
    fi

    if [ -n "$ZSH_BIN" ]; then
        try_shell "$ZSH_BIN" "$PROFILE"
        if [ "$RC" = "0" ] && printf '%s' "$OUT" | grep -Fq "EVAL=1"; then
            echo "ok: zsh still loads the integration"
        else
            echo "FAIL: zsh did not load the integration (rc=$RC): $OUT"; fail=1
        fi
    else
        echo "skip: zsh is not installed here, so it did not read the profile"
    fi
}

login_case "a login sh reads a fresh profile" 'export EDITOR=vi
'
n=$(grep -Fc 'nvx env' "$PROFILE" || true)
[ "$n" = "1" ] && echo "ok: one integration line" || { echo "FAIL: $n integration lines"; fail=1; }
before=$(cksum < "$PROFILE")
( eval "$(extract_setup)"; setup_profile "$PROFILE" "true" ) >/dev/null 2>&1
[ "$(cksum < "$PROFILE")" = "$before" ] && echo "ok: a second run changes nothing" || { echo "FAIL: a second run changed the profile"; fail=1; }
rm -rf "$HOME_DIR"; echo

# The profile earlier versions wrote, which every existing install has. Running
# the installer again has to repair it, or `sh -l` stays broken for them.
login_case "a login sh reads the profile an earlier installer wrote" 'export EDITOR=vi

# nvx (Node Version X-platform) shell integration
export PATH="$HOME/.nvx/bin:$PATH"
eval "$(nvx env)"
'
[ "$(grep -Fxc 'eval "$(nvx env)"' "$PROFILE" || true)" = "0" ] && echo "ok: the unguarded line is gone" || { echo "FAIL: the unguarded line is still there"; fail=1; }
grep -Fq 'export EDITOR=vi' "$PROFILE" && echo "ok: the rest of the profile is kept" || { echo "FAIL: the profile lost its other lines"; fail=1; }
if [ -f "$PROFILE.nvx-backup" ] && grep -Fxq 'eval "$(nvx env)"' "$PROFILE.nvx-backup"; then
    echo "ok: the old contents are saved"
else
    echo "FAIL: no backup of the old contents"; fail=1
fi
rm -rf "$HOME_DIR"; echo

# Only the exact line an earlier installer wrote is replaced. A line the user wrote
# is theirs, and a shell that cannot read it is theirs to fix, so no shell reads
# this one.
write_profile "lines the user wrote are left alone" 'export EDITOR=vi
# eval "$(nvx env)"
eval "$(nvx env --shell=zsh)"
'
grep -Fxq '# eval "$(nvx env)"' "$PROFILE" && grep -Fxq 'eval "$(nvx env --shell=zsh)"' "$PROFILE" \
    && echo "ok: a commented line and a different command are untouched" \
    || { echo "FAIL: a line the user wrote was changed"; fail=1; }
rm -rf "$HOME_DIR"; echo

# ---------------------------------------------------------------------------
# zsh: ~/.zprofile gets the PATH line, for `zsh -lc`, which reads it and not
# ~/.zshrc. The integration stays in ~/.zshrc, where an interactive shell runs it.
# ---------------------------------------------------------------------------
echo "=== zsh .zprofile gets the PATH line and nothing else ==="
HOME_DIR="$(mktemp -d)"; export HOME="$HOME_DIR"
( eval "$(extract_setup)"; setup_path_only "$HOME/.zprofile"; setup_path_only "$HOME/.zprofile" ) >/dev/null 2>&1
Z="$HOME_DIR/.zprofile"
[ "$(grep -Fc '.nvx/bin' "$Z" || true)" = "1" ] && echo "ok: one PATH line across two runs" || { echo "FAIL: PATH lines in .zprofile: $(grep -Fc '.nvx/bin' "$Z" || true)"; fail=1; }
if grep -Fq 'nvx env' "$Z"; then echo "FAIL: .zprofile holds the integration"; fail=1; else echo "ok: no integration line in .zprofile"; fi
make_nvx_stub "$HOME_DIR"
if [ -n "$ZSH_BIN" ]; then
    got=$(env -i HOME="$HOME_DIR" PATH=/usr/bin:/bin "$ZSH_BIN" -lc 'command -v nvx' 2>/dev/null || true)
    [ "$got" = "$HOME_DIR/.nvx/bin/nvx" ] && echo "ok: zsh -lc finds nvx" || { echo "FAIL: zsh -lc did not find nvx (got '$got')"; fail=1; }
else
    echo "skip: zsh is not installed here, so zsh -lc was not run"
fi
rm -rf "$HOME_DIR"; echo

if [ "$fail" = "0" ]; then echo "ALL CHECKS PASSED"; else echo "SOME CHECKS FAILED"; exit 1; fi
