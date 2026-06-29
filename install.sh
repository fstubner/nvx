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


# 2. Download Binary
# Check if local nvx binary exists (e.g. if running from source repo)
if [ -f "./nvx" ]; then
    echo "Copying local nvx binary to $BIN_DIR..."
    cp "./nvx" "$BIN_DIR/nvx"
elif [ -f "./$BINARY_NAME" ]; then
    echo "Copying local $BINARY_NAME to $BIN_DIR/nvx..."
    cp "./$BINARY_NAME" "$BIN_DIR/nvx"
else
    echo "Downloading nvx from $DOWNLOAD_URL..."
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$DOWNLOAD_URL" -o "$BIN_DIR/nvx"
        if curl -fsSL --fail "${DOWNLOAD_URL}.sha256" -o "$BIN_DIR/nvx.sha256" >/dev/null 2>&1; then
            echo "Verifying checksum..."
            EXPECTED_SHA=$(cat "$BIN_DIR/nvx.sha256" | awk '{print $1}')
            ACTUAL_SHA=$(shasum -a 256 "$BIN_DIR/nvx" | awk '{print $1}')
            if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
                echo "Error: Checksum verification failed!" >&2
                rm -f "$BIN_DIR/nvx" "$BIN_DIR/nvx.sha256"
                exit 1
            fi
            echo "Checksum verified successfully."
        else
            echo "Warning: Checksum file not available. Skipping verification."
        fi
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$BIN_DIR/nvx" "$DOWNLOAD_URL"
        if wget -qO "$BIN_DIR/nvx.sha256" "${DOWNLOAD_URL}.sha256" >/dev/null 2>&1; then
            echo "Verifying checksum..."
            EXPECTED_SHA=$(cat "$BIN_DIR/nvx.sha256" | awk '{print $1}')
            ACTUAL_SHA=$(shasum -a 256 "$BIN_DIR/nvx" | awk '{print $1}')
            if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
                echo "Error: Checksum verification failed!" >&2
                rm -f "$BIN_DIR/nvx" "$BIN_DIR/nvx.sha256"
                exit 1
            fi
            echo "Checksum verified successfully."
        else
            echo "Warning: Checksum file not available. Skipping verification."
        fi
    else
        echo "Error: Neither curl nor wget was found. Please install one of them." >&2
        exit 1
    fi
fi

chmod +x "$BIN_DIR/nvx"

# 3. Add to shell profiles
SHELL_NAME="$(basename "$SHELL")"
INTEGRATION_LINE='eval "$(nvx env)"'

setup_profile() {
    PROFILE_FILE="$1"
    CREATE_IF_MISSING="$2"
    if [ -f "$PROFILE_FILE" ] || [ "$CREATE_IF_MISSING" = "true" ]; then
        if [ ! -f "$PROFILE_FILE" ]; then
            touch "$PROFILE_FILE"
        fi
        if ! grep -q "nvx env" "$PROFILE_FILE"; then
            echo "Adding shell integration to $PROFILE_FILE..."
            echo "" >> "$PROFILE_FILE"
            echo "# nvx (Node Version X-platform) shell integration" >> "$PROFILE_FILE"

            echo "$INTEGRATION_LINE" >> "$PROFILE_FILE"
        fi
    fi
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
echo "Please restart your shell or run the following to apply:"
echo "  export PATH=\"\$HOME/.nvx/bin:\$PATH\""
echo "  $INTEGRATION_LINE"
