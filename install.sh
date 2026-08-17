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
        ARCH_LABEL="amd64"
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
    echo "Downloading nvx from $DOWNLOAD_URL..."
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$DOWNLOAD_URL" -o "$BIN_DIR/nvx"
        if curl -fsSL --fail "${DOWNLOAD_URL}.sha256" -o "$BIN_DIR/nvx.sha256" >/dev/null 2>&1; then
            echo "Verifying checksum..."
            EXPECTED_SHA=$(cat "$BIN_DIR/nvx.sha256" | awk '{print $1}')
            ACTUAL_SHA=$(compute_sha256 "$BIN_DIR/nvx")
            if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
                echo "Error: Checksum verification failed!" >&2
                rm -f "$BIN_DIR/nvx" "$BIN_DIR/nvx.sha256"
                exit 1
            fi
            echo "Checksum verified successfully."
        else
            if [ "${NVX_INSECURE_SKIP_CHECKSUM:-}" = "1" ]; then
                echo "Warning: Checksum file not available. Skipping verification because NVX_INSECURE_SKIP_CHECKSUM=1."
            else
                echo "Error: Checksum file not available. Refusing to install without verification." >&2
                rm -f "$BIN_DIR/nvx" "$BIN_DIR/nvx.sha256"
                exit 1
            fi
        fi
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$BIN_DIR/nvx" "$DOWNLOAD_URL"
        if wget -qO "$BIN_DIR/nvx.sha256" "${DOWNLOAD_URL}.sha256" >/dev/null 2>&1; then
            echo "Verifying checksum..."
            EXPECTED_SHA=$(cat "$BIN_DIR/nvx.sha256" | awk '{print $1}')
            ACTUAL_SHA=$(compute_sha256 "$BIN_DIR/nvx")
            if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
                echo "Error: Checksum verification failed!" >&2
                rm -f "$BIN_DIR/nvx" "$BIN_DIR/nvx.sha256"
                exit 1
            fi
            echo "Checksum verified successfully."
        else
            if [ "${NVX_INSECURE_SKIP_CHECKSUM:-}" = "1" ]; then
                echo "Warning: Checksum file not available. Skipping verification because NVX_INSECURE_SKIP_CHECKSUM=1."
            else
                echo "Error: Checksum file not available. Refusing to install without verification." >&2
                rm -f "$BIN_DIR/nvx" "$BIN_DIR/nvx.sha256"
                exit 1
            fi
        fi
    else
        echo "Error: Neither curl nor wget was found. Please install one of them." >&2
        exit 1
    fi
fi

chmod +x "$BIN_DIR/nvx"

# 3. Add to shell profiles
SHELL_NAME="$(basename "$SHELL")"
MARKER_LINE='# nvx (Node Version X-platform) shell integration'
# Single-quoted so $HOME and $PATH reach the profile unexpanded and resolve at
# shell startup. Guarded against re-entry because the block is written to both an
# interactive and a login profile, and on most systems the login one sources the
# interactive one -- an unguarded export would then add the directory to PATH twice
# per shell, compounding in nested shells.
PATH_LINE='case ":$PATH:" in *":$HOME/.nvx/bin:"*) ;; *) export PATH="$HOME/.nvx/bin:$PATH" ;; esac'
INTEGRATION_LINE='eval "$(nvx env)"'

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
setup_profile() {
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
        # covers both cases.
        setup_profile "$HOME/.zshrc" "true"
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
