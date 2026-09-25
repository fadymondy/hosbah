#!/usr/bin/env bash
set -euo pipefail

# Installs MariaDB server from the distro's own apt repository — a drop-in, actively maintained
# replacement for MySQL that Debian and Ubuntu both ship directly, so no third-party repository is
# needed (https://mariadb.org/download/). Idempotent: re-running is a no-op once installed and
# running.

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
apt-get install -y --no-install-recommends mariadb-server

systemctl enable --now mariadb

mysql --version
echo "mariadb installed and running; secure it with mariadb-secure-installation before exposing it"
