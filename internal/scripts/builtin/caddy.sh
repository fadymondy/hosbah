#!/usr/bin/env bash
set -euo pipefail

# Installs Caddy from its own apt repository (https://caddyserver.com/docs/install#debian-ubuntu-raspbian).
# Idempotent: re-running just refreshes the package. Clean-room, from Caddy's public docs.

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
apt-get install -y --no-install-recommends debian-keyring debian-archive-keyring apt-transport-https curl gnupg

install -d -m 0755 /usr/share/keyrings
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list

apt-get update -y
apt-get install -y --no-install-recommends caddy

caddy version
echo "caddy installed: $(caddy version)"
