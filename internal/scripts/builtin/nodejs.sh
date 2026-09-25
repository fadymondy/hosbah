#!/usr/bin/env bash
set -euo pipefail

# Installs Node.js from NodeSource's apt repository (https://github.com/nodesource/distributions),
# the standard source of current Node.js packages for Debian/Ubuntu. HOSBAH_PARAM_VERSION selects
# the major line (18, 20, 22 or 24); the control plane validates it against that enum first.
# Idempotent: re-running just refreshes the packages.

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

VERSION="${HOSBAH_PARAM_VERSION:-22}"
case "$VERSION" in
  18|20|22|24) ;;
  *)
    echo "unsupported Node.js version: $VERSION (expected 18, 20, 22 or 24)" >&2
    exit 1
    ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends ca-certificates curl gnupg

install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg
echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${VERSION}.x nodistro main" \
  > /etc/apt/sources.list.d/nodesource.list

apt-get update -y
apt-get install -y --no-install-recommends nodejs

node -v
npm -v
echo "node.js ${VERSION}.x installed"
