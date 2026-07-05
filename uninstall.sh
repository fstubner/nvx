#!/usr/bin/env sh
# nvx uninstaller (Unix). Removes the nvx home, shims, and shell integration.
set -e

NVX_HOME="${NVX_HOME:-$HOME/.nvx}"

echo "This will remove nvx:"
echo "  - directory: $NVX_HOME (installed runtimes, shims, policy, cache)"
echo "  - the 'eval \"\$(nvx env)\"' line from your shell profiles"
printf "Proceed? [y/N] "
read -r ans
case "$ans" in
  y|Y|yes|YES) ;;
  *) echo "Aborted."; exit 0 ;;
esac

# Strip the integration line from known shell profiles.
for profile in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile" "$HOME/.bash_profile"; do
  if [ -f "$profile" ] && grep -q 'nvx env' "$profile" 2>/dev/null; then
    tmp="$profile.nvx-uninstall.tmp"
    grep -v 'nvx env' "$profile" > "$tmp" && mv "$tmp" "$profile"
    echo "Removed nvx integration from $profile"
  fi
done

# Remove the nvx home (runtimes, shims, cache, policy).
if [ -d "$NVX_HOME" ]; then
  rm -rf "$NVX_HOME"
  echo "Removed $NVX_HOME"
fi

# Remove the binary if it lives on PATH and is writable.
if command -v nvx >/dev/null 2>&1; then
  BIN="$(command -v nvx)"
  if [ -w "$BIN" ]; then
    rm -f "$BIN" && echo "Removed $BIN"
  else
    echo "Note: remove the nvx binary manually (no write permission): $BIN"
  fi
fi

echo "nvx uninstalled. Restart your shell to finish."
