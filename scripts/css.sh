#!/bin/sh
# Rebuild web/static/site.css from web/src/input.css.
#
# Tailwind's standalone binary is used deliberately: it needs no node, no
# package.json and no lockfile, so the only thing this repository gains from
# Tailwind is a stylesheet. The built CSS is committed, which is what lets the
# container image build with nothing but Go — the Dockerfile never sees this.
#
# Run it after changing a template, or the classes you just wrote will not
# exist in the stylesheet.
set -e
BIN="${TAILWIND:-$HOME/development/tailwindcss}"
if [ ! -x "$BIN" ]; then
  echo "tailwindcss not found at $BIN" >&2
  echo "get it: https://github.com/tailwindlabs/tailwindcss/releases/latest (tailwindcss-linux-x64)" >&2
  exit 1
fi
cd "$(dirname "$0")/.."
"$BIN" -i web/src/input.css -o web/static/site.css --minify
echo "built web/static/site.css ($(wc -c < web/static/site.css) bytes)"
