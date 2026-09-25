#!/usr/bin/env bash
set -euo pipefail

# Installs Chromium (headless-capable) plus the shared libraries it and most headless-browser
# tooling (Puppeteer, Playwright) need on a minimal Debian/Ubuntu host, from the distro's own apt
# repository. Idempotent: re-running is a no-op once installed. See
# https://wiki.debian.org/Chromium and https://packages.ubuntu.com/chromium.

if [ ! -r /etc/os-release ]; then
  echo "cannot detect the Linux distribution (no /etc/os-release)" >&2
  exit 1
fi
. /etc/os-release
case "$ID" in
  debian|ubuntu) ;;
  *)
    echo "this script supports Debian and Ubuntu only (found: $ID)" >&2
    exit 1
    ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  chromium \
  ca-certificates fonts-liberation \
  libasound2 libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 libcups2 libdbus-1-3 \
  libdrm2 libgbm1 libgtk-3-0 libnspr4 libnss3 libx11-xcb1 libxcomposite1 \
  libxdamage1 libxfixes3 libxkbcommon0 libxrandr2 xdg-utils

chromium --version || chromium-browser --version
echo "chromium and its headless-run dependencies are installed"
