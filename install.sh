#!/bin/sh
set -e

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
esac

URL="https://github.com/btraven00/obadm/releases/download/nightly/obadm_${OS}_${ARCH}"
curl -fsSL "$URL" -o obadm
chmod +x obadm
echo "obadm downloaded to $(pwd)/obadm"
