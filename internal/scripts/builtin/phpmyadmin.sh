#!/usr/bin/env bash
set -euo pipefail

# Installs phpMyAdmin from its official release tarball (https://www.phpmyadmin.net/downloads/)
# under /usr/share/phpmyadmin, served by an existing PHP-FPM + Caddy/nginx setup — this script does
# not write any web-server config, only unpacks the app and creates its own tmp/config directories.
# Needs php-cli (php.sh) to be present for phpMyAdmin's own PHP requirement; does not install a
# database itself. Idempotent: re-running replaces the unpacked copy with the current release.

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
apt-get install -y --no-install-recommends curl ca-certificates tar

VERSION="${HOSBAH_PARAM_VERSION:-5.2.1}"
DEST="/usr/share/phpmyadmin"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "https://files.phpmyadmin.net/phpMyAdmin/${VERSION}/phpMyAdmin-${VERSION}-all-languages.tar.gz" -o "$TMP/pma.tar.gz"
tar -xzf "$TMP/pma.tar.gz" -C "$TMP"

rm -rf "$DEST"
mv "$TMP/phpMyAdmin-${VERSION}-all-languages" "$DEST"
install -d -m 0750 -o www-data -g www-data "$DEST/tmp"
chown -R root:root "$DEST"
chown -R www-data:www-data "$DEST/tmp"

echo "phpmyadmin ${VERSION} unpacked at $DEST — point a site or proxy host at it and set \$cfg['blowfish_secret']"
