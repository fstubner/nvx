#!/bin/sh
# install.sh
# Installer script for nvx (Node Version X-platform) on macOS and Linux


set -e

NVX_HOME="$HOME/.nvx"
BIN_DIR="$NVX_HOME/bin"

echo "Setting up nvx directories..."
mkdir -p "$BIN_DIR"
mkdir -p "$NVX_HOME/versions/node"

# 1. Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64)
        ARCH_LABEL="amd64"
        ;;
    arm64|aarch64)
        ARCH_LABEL="arm64"
        ;;
    *)
        # Releases are built for amd64 and arm64 only. This used to fall back
        # to amd64, which installs a binary that cannot run on this machine.
        echo "Error: unsupported CPU architecture '$ARCH'. nvx is released for x86_64 and arm64 only." >&2
        exit 1
        ;;
esac

BINARY_NAME="nvx-$OS-$ARCH_LABEL"
DOWNLOAD_URL="https://github.com/fstubner/nvx/releases/latest/download/$BINARY_NAME"

# sha256 helper: shasum (macOS, perl-based) is absent on minimal Linux images,
# where coreutils provides sha256sum instead.
compute_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}


# 2. Download Binary
# Check if local nvx binary exists (e.g. if running from source repo)
if [ "${NVX_USE_LOCAL_BINARY:-}" = "1" ] && [ -f "./nvx" ]; then
    echo "Copying local nvx binary to $BIN_DIR..."
    cp "./nvx" "$BIN_DIR/nvx"
elif [ "${NVX_USE_LOCAL_BINARY:-}" = "1" ] && [ -f "./$BINARY_NAME" ]; then
    echo "Copying local $BINARY_NAME to $BIN_DIR/nvx..."
    cp "./$BINARY_NAME" "$BIN_DIR/nvx"
else
    # fetch <url> <file>: curl or wget, failing on an HTTP error.
    if command -v curl >/dev/null 2>&1; then
        fetch() { curl -fsSL "$1" -o "$2"; }
    elif command -v wget >/dev/null 2>&1; then
        fetch() { wget -qO "$2" "$1"; }
    else
        echo "Error: Neither curl nor wget was found. Please install one of them." >&2
        exit 1
    fi
    # Checked before downloading. Without either tool the hash came out empty,
    # and the error said the checksum did not match.
    if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
        echo "Error: Neither sha256sum nor shasum was found, so the download cannot be verified." >&2
        echo "Install coreutils (for sha256sum) or perl (for shasum)." >&2
        exit 1
    fi

    # The download goes beside nvx, not over it, and replaces it only once its
    # checksum matches. It used to be written straight to $BIN_DIR/nvx, so a
    # mismatch deleted the file it had just replaced: an upgrade that failed
    # its check left no nvx at all, where the previous one had been working.
    # install.ps1 does the same (Install-NvxDownloadedBinary).
    DOWNLOAD_PATH="$BIN_DIR/nvx.download"
    SUMS_PATH="$BIN_DIR/nvx.sha256"
    rm -f "$DOWNLOAD_PATH" "$SUMS_PATH"
    echo "Downloading nvx from $DOWNLOAD_URL..."
    if ! fetch "$DOWNLOAD_URL" "$DOWNLOAD_PATH"; then
        rm -f "$DOWNLOAD_PATH"
        echo "Error: could not download $DOWNLOAD_URL" >&2
        exit 1
    fi
    if fetch "${DOWNLOAD_URL}.sha256" "$SUMS_PATH" >/dev/null 2>&1; then
        echo "Verifying checksum..."
        EXPECTED_SHA=$(awk 'NR==1 {print $1}' "$SUMS_PATH")
        ACTUAL_SHA=$(compute_sha256 "$DOWNLOAD_PATH")
        rm -f "$SUMS_PATH"
        if [ -z "$EXPECTED_SHA" ] || [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
            rm -f "$DOWNLOAD_PATH"
            echo "Error: Checksum verification failed! nvx was not changed." >&2
            exit 1
        fi
        echo "Checksum verified successfully."
    elif [ "${NVX_INSECURE_SKIP_CHECKSUM:-}" = "1" ]; then
        rm -f "$SUMS_PATH"
        echo "Warning: Checksum file not available. Skipping verification because NVX_INSECURE_SKIP_CHECKSUM=1."
    else
        rm -f "$DOWNLOAD_PATH" "$SUMS_PATH"
        echo "Error: Checksum file not available. Refusing to install without verification." >&2
        exit 1
    fi

    # Second check, when gh can make it. The .sha256 file comes from the same
    # release as the binary, so it cannot tell a replaced release from a real
    # one. The build attestation is signed by release.yml and checked against
    # GitHub. Only an attestation made by release.yml counts, as in
    # scripts/release/lib.sh: without --signer-workflow, one from any workflow
    # in this repository would pass.
    #
    # gh has to be signed in and new enough. The attestation command arrived in
    # gh 2.49 and --signer-workflow in 2.51, so the help text is asked for the
    # flag. A gh that cannot make the check skips it. A gh that makes it and
    # reports a failure, or cannot reach GitHub, stops the install.
    SIGNER_WORKFLOW="fstubner/nvx/.github/workflows/release.yml"
    VERIFY_HINT="gh attestation verify $BIN_DIR/nvx --repo fstubner/nvx --signer-workflow $SIGNER_WORKFLOW"
    if ! command -v gh >/dev/null 2>&1; then
        echo "Provenance check skipped: gh is not installed, so nothing has shown this download came from the release workflow."
        echo "  The checksum only shows it matches its release page. To check by hand, install gh 2.51 or newer,"
        echo "  sign in with 'gh auth login', then run: $VERIFY_HINT"
    elif ! gh attestation verify --help 2>&1 | grep -q -- '--signer-workflow' || ! gh auth status >/dev/null 2>&1; then
        echo "Provenance check skipped: gh is older than 2.51 or not signed in, so nothing has shown this download came from the release workflow."
        echo "  The checksum only shows it matches its release page. To check by hand, update gh,"
        echo "  sign in with 'gh auth login', then run: $VERIFY_HINT"
    else
        echo "Verifying build provenance..."
        if PROVENANCE_OUT=$(gh attestation verify "$DOWNLOAD_PATH" --repo fstubner/nvx --signer-workflow "$SIGNER_WORKFLOW" 2>&1); then
            echo "Build provenance verified."
        else
            rm -f "$DOWNLOAD_PATH"
            printf '%s\n' "$PROVENANCE_OUT" >&2
            echo "Error: Provenance verification failed! nvx was not changed." >&2
            exit 1
        fi
    fi
    mv -f "$DOWNLOAD_PATH" "$BIN_DIR/nvx"
fi

chmod +x "$BIN_DIR/nvx"

# The shims are what put nvx in front of npm, node and the rest. Nothing else
# wrote them until a shell profile first ran `nvx env`, so a shell that never
# loads the profile (an agent started from a GUI app, cron, CI) had nvx on PATH
# and nothing to intercept with. They are written here, before the profile is
# touched, so they exist whenever PATH takes effect and a failure leaves the
# profile as it was. Run from /, because `init-shims` also writes shims for the
# project it is run in.
if ! ( cd / && "$BIN_DIR/nvx" init-shims ); then
    echo "Error: creating the nvx shims failed, so your shell profile was not changed. nvx is in $BIN_DIR." >&2
    echo "  Run '$BIN_DIR/nvx init-shims' to see why." >&2
    exit 1
fi

# 3. Add to shell profiles
SHELL_NAME="$(basename "$SHELL")"
MARKER_LINE='# nvx (Node Version X-platform) shell integration'
# Single-quoted so $HOME and $PATH reach the profile unexpanded and resolve at
# shell startup. Guarded against re-entry because the block is written to both an
# interactive and a login profile, and on most systems the login one sources the
# interactive one -- an unguarded export would then add the directory to PATH twice
# per shell, compounding in nested shells.
PATH_LINE='case ":$PATH:" in *":$HOME/.nvx/bin:"*) ;; *) export PATH="$HOME/.nvx/bin:$PATH" ;; esac'
# Only bash and zsh get the integration. `nvx env` prints bash syntax for anything
# that is not zsh or fish, and ~/.profile is read by every login sh, which on
# Debian and Ubuntu is dash. dash stops at ${PATH//...} with "Bad substitution",
# so `sh -l` aborted for as long as nvx was installed. The PATH line above is plain
# POSIX and stays unguarded, so a login sh still finds the shims.
INTEGRATION_LINE='if [ -n "${BASH_VERSION:-}${ZSH_VERSION:-}" ]; then eval "$(nvx env)"; fi'
# What earlier versions of this installer wrote. See guard_old_integration_line.
OLD_INTEGRATION_LINE='eval "$(nvx env)"'

# profile_has_path_line matches any nvx bin PATH entry, not one exact string, so a
# profile written by an earlier installer (or edited by hand) is recognised instead
# of being given a second, redundant line.
profile_has_path_line() {
    grep -Fq '.nvx/bin' "$1"
}

# bash_login_profile picks the file a bash LOGIN shell will actually read, in bash's
# own precedence order: .bash_profile, then .bash_login, then .profile.
#
# This matters most on macOS, where Terminal starts bash as a login shell -- which
# reads none of .bashrc. Writing only to .bashrc there means nvx never activates.
#
# It only creates .bash_profile when none of the three exist. Creating one while
# .profile is present would be actively harmful: bash reads the FIRST match and
# ignores the rest, so a new .bash_profile silently stops .profile from ever being
# read, discarding whatever environment the user kept there.
bash_login_profile() {
    if [ -f "$HOME/.bash_profile" ]; then
        echo "$HOME/.bash_profile"
    elif [ -f "$HOME/.bash_login" ]; then
        echo "$HOME/.bash_login"
    elif [ -f "$HOME/.profile" ]; then
        echo "$HOME/.profile"
    else
        echo "$HOME/.bash_profile"
    fi
}

# The PATH export MUST precede the eval. Earlier versions of this installer wrote
# only the eval, so on the next shell `nvx` was not resolvable, the eval emitted
# "nvx: command not found", and nvx never activated -- on every new shell, forever.
# Appending the export after the eval does not fix that: the eval still runs first
# and still fails. So an existing profile is repaired by inserting the line above
# the eval rather than appending to the end.
setup_profile_block() {
    PROFILE_FILE="$1"
    CREATE_IF_MISSING="$2"

    if [ ! -f "$PROFILE_FILE" ] && [ "$CREATE_IF_MISSING" != "true" ]; then
        return 0
    fi
    if [ ! -f "$PROFILE_FILE" ]; then
        touch "$PROFILE_FILE"
    fi

    if profile_has_path_line "$PROFILE_FILE"; then
        # Already correct. Only add the eval if something removed it.
        if ! grep -q "nvx env" "$PROFILE_FILE"; then
            printf '%s\n' "$INTEGRATION_LINE" >> "$PROFILE_FILE"
        fi
        return 0
    fi

    if grep -q "nvx env" "$PROFILE_FILE"; then
        echo "Repairing nvx shell integration in $PROFILE_FILE..."
        # This edits a file the user owns and did not ask us to rewrite, so keep a
        # copy. Losing a shell profile is not recoverable from here.
        cp "$PROFILE_FILE" "$PROFILE_FILE.nvx-backup"
        TMP_PROFILE="$PROFILE_FILE.nvx-tmp.$$"
        awk -v pathline="$PATH_LINE" '
            !inserted && index($0, "nvx env") { print pathline; inserted = 1 }
            { print }
        ' "$PROFILE_FILE" > "$TMP_PROFILE"

        if [ -s "$TMP_PROFILE" ] && profile_has_path_line "$TMP_PROFILE"; then
            # Write through the original file rather than mv, so its permissions
            # and ownership survive; a profile that becomes 0600 or root-owned is
            # its own outage.
            cat "$TMP_PROFILE" > "$PROFILE_FILE"
            rm -f "$TMP_PROFILE"
            echo "  (previous contents saved to $PROFILE_FILE.nvx-backup)"
        else
            rm -f "$TMP_PROFILE"
            echo "Warning: could not repair $PROFILE_FILE automatically." >&2
            echo "Add this line immediately above the nvx eval:" >&2
            echo "  $PATH_LINE" >&2
        fi
        return 0
    fi

    echo "Adding shell integration to $PROFILE_FILE..."
    printf '\n%s\n%s\n%s\n' "$MARKER_LINE" "$PATH_LINE" "$INTEGRATION_LINE" >> "$PROFILE_FILE"
}

# A profile written by an earlier installer holds the eval on a line of its own,
# with no guard, and a login sh that reads it aborts (see INTEGRATION_LINE).
# Running the installer again replaces that exact line with the guarded one. Any
# other line, such as one the user wrote or wrapped in their own condition, is left
# alone.
guard_old_integration_line() {
    PROFILE_FILE="$1"
    if [ ! -f "$PROFILE_FILE" ] || ! grep -Fxq "$OLD_INTEGRATION_LINE" "$PROFILE_FILE"; then
        return 0
    fi
    echo "Updating the nvx line in $PROFILE_FILE so a login sh can read it..."
    # A copy of the file as it was before this installer touched it. The repair
    # above may have made one already, and that one is the older.
    [ -e "$PROFILE_FILE.nvx-backup" ] || cp "$PROFILE_FILE" "$PROFILE_FILE.nvx-backup"
    TMP_PROFILE="$PROFILE_FILE.nvx-tmp.$$"
    awk -v old="$OLD_INTEGRATION_LINE" -v new="$INTEGRATION_LINE" '
        $0 == old { print new; next }
        { print }
    ' "$PROFILE_FILE" > "$TMP_PROFILE"
    if [ -s "$TMP_PROFILE" ] && grep -Fxq "$INTEGRATION_LINE" "$TMP_PROFILE"; then
        cat "$TMP_PROFILE" > "$PROFILE_FILE"
        rm -f "$TMP_PROFILE"
        echo "  (previous contents saved to $PROFILE_FILE.nvx-backup)"
    else
        rm -f "$TMP_PROFILE"
        echo "Warning: could not update $PROFILE_FILE automatically." >&2
        echo "Replace the line $OLD_INTEGRATION_LINE with:" >&2
        echo "  $INTEGRATION_LINE" >&2
    fi
}

setup_profile() {
    setup_profile_block "$1" "$2"
    guard_old_integration_line "$1"
}

# Puts nvx on PATH in a file and does nothing else.
setup_path_only() {
    PROFILE_FILE="$1"
    if [ -f "$PROFILE_FILE" ] && profile_has_path_line "$PROFILE_FILE"; then
        return 0
    fi
    echo "Adding nvx to PATH in $PROFILE_FILE..."
    printf '\n%s\n%s\n' "$MARKER_LINE" "$PATH_LINE" >> "$PROFILE_FILE"
}

# fish never reads ~/.profile and cannot run the POSIX lines above, so it gets
# its own file in conf.d, which fish reads at every start and nothing else
# writes to. PATH is set for every fish, scripts included, and the integration
# only for interactive ones.
# `nvx doctor --fix` writes the same text (fishConfDContent in
# internal/nvx/shell_profile.go), and a Go test compares it with the printf
# lines below, so change both together.
setup_fish() {
    FISH_CONF="${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/nvx.fish"
    if [ -f "$FISH_CONF" ] && grep -q "nvx env" "$FISH_CONF"; then
        return 0
    fi
    echo "Adding shell integration to $FISH_CONF..."
    mkdir -p "$(dirname "$FISH_CONF")"
    {
        printf '%s\n' "$MARKER_LINE"
        printf '%s\n' 'if not contains $HOME/.nvx/bin $PATH'
        printf '%s\n' '    set -gx PATH $HOME/.nvx/bin $PATH'
        printf '%s\n' 'end'
        printf '%s\n' 'if status is-interactive'
        printf '%s\n' '    nvx env --shell=fish | source'
        printf '%s\n' 'end'
    } >> "$FISH_CONF"
}

case "$SHELL_NAME" in
    bash)
        # Interactive non-login shells (the common case on Linux) read .bashrc;
        # login shells (the common case on macOS) read none of it. Both are needed.
        setup_profile "$HOME/.bashrc" "true"
        BASH_LOGIN_FILE="$(bash_login_profile)"
        if [ "$BASH_LOGIN_FILE" != "$HOME/.bashrc" ]; then
            setup_profile "$BASH_LOGIN_FILE" "true"
        fi
        ;;
    zsh)
        # zsh reads .zshrc for every interactive shell, login or not, so one file
        # covers both of those cases.
        setup_profile "$HOME/.zshrc" "true"
        # It does not read .zshrc for a login shell that runs one command. `zsh -lc`
        # is how many tools start a shell, and it reads .zprofile and not .zshrc, so
        # a tool started from a GUI app, cron or CI got no PATH. Only the PATH line
        # goes here. The integration needs an interactive shell, which .zshrc
        # already serves. Not .zshenv, because every zsh reads that one, scripts
        # included.
        setup_path_only "$HOME/.zprofile"
        ;;
    fish)
        # This branch used to write the POSIX lines to ~/.profile anyway and
        # report the profile as updated.
        setup_fish
        echo ""
        echo "nvx has been successfully installed!"
        echo "New fish sessions pick it up automatically. To use nvx in THIS shell without restarting it, run:"
        echo "  source $FISH_CONF"
        exit 0
        ;;
    *)
        setup_profile "$HOME/.profile" "true"
        ;;
esac


echo ""
echo "nvx has been successfully installed!"
echo "Your shell profile has been updated, so new shells pick it up automatically."
echo "To use nvx in THIS shell without restarting it, run:"
echo "  $PATH_LINE"
echo "  $INTEGRATION_LINE"
