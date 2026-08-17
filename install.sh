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
# shell startup.
PATH_LINE='export PATH="$HOME/.nvx/bin:$PATH"'
INTEGRATION_LINE='eval "$(nvx env)"'

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

    if grep -Fq "$PATH_LINE" "$PROFILE_FILE"; then
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

        if [ -s "$TMP_PROFILE" ] && grep -Fq "$PATH_LINE" "$TMP_PROFILE"; then
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
        setup_profile "$HOME/.bashrc" "true"
        setup_profile "$HOME/.bash_profile" "false"
        ;;
    zsh)
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
