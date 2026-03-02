#!/bin/sh
set -e

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
esac

DEST="$HOME/.obadm/bin/obadm"
mkdir -p "$HOME/.obadm/bin"

URL="https://github.com/btraven00/obadm/releases/download/nightly/obadm_${OS}_${ARCH}"
echo "Downloading obadm..."
curl -fsSL "$URL" -o "$DEST"
chmod +x "$DEST"
echo "Installed: $DEST"

# Append PATH export to a shell rc file, with permission.
add_to_path() {
  RC="$1"
  LINE='export PATH="$HOME/.obadm/bin:$PATH"'

  # Skip if already present.
  if [ -f "$RC" ] && grep -qF '.obadm/bin' "$RC"; then
    echo "$RC: PATH entry already present, skipping."
    return
  fi

  printf 'Add %s to PATH in %s? [y/N] ' '$HOME/.obadm/bin' "$RC"
  read -r REPLY
  case "$REPLY" in
    [yY]|[yY][eE][sS])
      printf '\n# obadm\n%s\n' "$LINE" >> "$RC"
      echo "Updated $RC."
      ;;
    *)
      echo "Skipped $RC."
      ;;
  esac
}

# Detect active shell and offer the matching rc file.
SHELL_NAME=$(basename "${SHELL:-sh}")
case "$SHELL_NAME" in
  zsh)  add_to_path "$HOME/.zshrc" ;;
  bash) add_to_path "$HOME/.bashrc" ;;
  *)
    echo "Unknown shell ($SHELL_NAME). Add the following to your shell rc manually:"
    echo '  export PATH="$HOME/.obadm/bin:$PATH"'
    ;;
esac

echo ""
echo "Run 'obadm --help' after opening a new shell (or: source the rc file above)."
