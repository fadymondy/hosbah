#!/usr/bin/env bash
set -euo pipefail

# Installs Composer system-wide from the official installer, verified with the signature published
# at https://composer.github.io/installer.sig (https://getcomposer.org/download/). Idempotent:
# re-running replaces the binary with the current release. Needs php-cli on PATH (the php.sh
# built-in, or the distro's own php-cli).

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

if ! command -v php >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get install -y --no-install-recommends php-cli curl
fi

EXPECTED="$(curl -fsSL https://composer.github.io/installer.sig)"
curl -fsSL https://getcomposer.org/installer -o /tmp/composer-setup.php
ACTUAL="$(php -r "echo hash_file('sha384', '/tmp/composer-setup.php');")"
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "composer installer signature mismatch — refusing to run it" >&2
  rm -f /tmp/composer-setup.php
  exit 1
fi

php /tmp/composer-setup.php --install-dir=/usr/local/bin --filename=composer
rm -f /tmp/composer-setup.php

composer --version
echo "composer installed at /usr/local/bin/composer"
