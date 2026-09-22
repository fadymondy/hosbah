#!/usr/bin/env bash
# Publishes each SDK to its own public repository, github.com/fadymondy/hosbah-{lang}.
# The main repository stays the source of truth: sdk/{node,python,php} and pkg/hosbah (Go) are
# copied as they are, plus a LICENSE, and committed as "sync from fadymondy/hosbah@<sha>".
# Re-run after changing an SDK. Needs `gh` logged in with repo scope.
#
#   scripts/publish-sdks.sh            # all
#   scripts/publish-sdks.sh node go    # some
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
sha="$(git -C "$root" rev-parse --short HEAD)"
owner="fadymondy"
langs=("$@")
[ ${#langs[@]} -eq 0 ] && langs=(node python php go)

license() {
  cat <<EOF
MIT License

Copyright (c) $(date +%Y) Fady Mondy

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
EOF
}

describe() {
  case "$1" in
    node) echo "Hosbah SDK for Node.js and TypeScript" ;;
    python) echo "Hosbah SDK for Python" ;;
    php) echo "Hosbah SDK for PHP and Laravel" ;;
    go) echo "Hosbah SDK for Go" ;;
  esac
}

for lang in "${langs[@]}"; do
  repo="$owner/hosbah-$lang"
  work="$(mktemp -d)"
  if gh repo view "$repo" >/dev/null 2>&1; then
    gh repo clone "$repo" "$work" -- -q
  else
    gh repo create "$repo" --public --description "$(describe "$lang") — manage servers, apps, domains and proxy hosts on hosbah.com" --homepage https://hosbah.com >/dev/null
    git -C "$work" init -q -b main
    git -C "$work" remote add origin "https://github.com/$repo.git"
  fi
  # Replace the tree (keep .git) with the current SDK.
  find "$work" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
  if [ "$lang" = go ]; then
    cp "$root"/pkg/hosbah/*.go "$work"/
    cp "$root/sdk/go/README.md" "$root/sdk/go/go.mod" "$work"/
  else
    (cd "$root/sdk/$lang" && git ls-files -z . | xargs -0 -I{} cp --parents {} "$work"/)
  fi
  license > "$work/LICENSE"
  git -C "$work" add -A
  if git -C "$work" diff --cached --quiet; then
    echo "$repo: unchanged"
  else
    git -C "$work" -c user.name="Fady Mondy" -c user.email="info@3x1.io" commit -q -m "sync from fadymondy/hosbah@$sha"
    git -C "$work" push -q -u origin main
    echo "$repo: pushed ($sha)"
  fi
  rm -rf "$work"
done
