#!/usr/bin/env bash
set -euo pipefail

# Installs PHP-FPM and a common extension set from ondrej/sury's apt repository
# (https://deb.sury.org/), the standard source of current PHP packages for Debian/Ubuntu.
# HOSBAH_PARAM_VERSION selects the PHP series (8.1, 8.2, 8.3 or 8.4); the control plane validates
# it against that enum before this ever runs, so it is safe to use directly in a package name.
# Idempotent: re-running just refreshes the packages. Clean-room, from Ondřej Surý's public docs.

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

VERSION="${HOSBAH_PARAM_VERSION:-8.3}"
case "$VERSION" in
  8.1|8.2|8.3|8.4) ;;
  *)
    echo "unsupported PHP version: $VERSION (expected 8.1, 8.2, 8.3 or 8.4)" >&2
    exit 1
    ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends ca-certificates curl gnupg apt-transport-https lsb-release

install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://packages.sury.org/php/apt.gpg -o /etc/apt/keyrings/deb.sury.org-php.gpg
echo "deb [signed-by=/etc/apt/keyrings/deb.sury.org-php.gpg] https://packages.sury.org/php/ $(lsb_release -sc) main" \
  > /etc/apt/sources.list.d/php.list

apt-get update -y
apt-get install -y --no-install-recommends \
  "php${VERSION}-fpm" "php${VERSION}-cli" "php${VERSION}-common" "php${VERSION}-mysql" \
  "php${VERSION}-pgsql" "php${VERSION}-curl" "php${VERSION}-mbstring" "php${VERSION}-xml" \
  "php${VERSION}-zip" "php${VERSION}-gd" "php${VERSION}-intl" "php${VERSION}-bcmath" "php${VERSION}-opcache"

systemctl enable --now "php${VERSION}-fpm"
"php${VERSION}" -v
echo "php ${VERSION} installed and php${VERSION}-fpm is running"
