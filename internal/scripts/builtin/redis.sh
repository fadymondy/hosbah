#!/usr/bin/env bash
set -euo pipefail

# Installs Redis server from the distro's own apt repository. Idempotent: re-running is a no-op
# once installed and running. See https://redis.io/docs/latest/operate/oss_and_stack/install/.

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
apt-get install -y --no-install-recommends redis-server

# Binds to loopback only by default on these packages; leave that as-is unless the operator has
# already changed it, this script only ensures the service is enabled and running.
systemctl enable --now redis-server

redis-cli ping || true
echo "redis installed and running on 127.0.0.1:6379"
