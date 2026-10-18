#!/usr/bin/env bash
# screenshots — capture the full page matrix with npx playwright.
#   usage: scripts/screenshots.sh [baseUrl] [outDir]
# Themes are driven through prefers-color-scheme emulation: with no stored
# theme, gerve's inline script maps prefers-light to html.light.
set -euo pipefail
BASE="${1:-http://127.0.0.1:8177}"
OUT="${2:-.screenshots}"
mkdir -p "$OUT"

# derive a commit SHA for the commit/side-by-side pages from the log page
SHA=$(curl -fsSL "$BASE/commits/HEAD" | grep -oE '[0-9a-f]{40}' | head -1)
[ -n "$SHA" ] || { echo "could not resolve a commit SHA from $BASE"; exit 1; }

pages=(
  "/tree/HEAD/|tree-root"
  "/tree/HEAD/internal/|tree-sub"
  "/blob/HEAD/README.md|blob-md"
  "/blob/HEAD/README.md?source=1|blob-md-source"
  "/blob/HEAD/cmd/gerve/main.go|blob-code"
  "/blame/HEAD/cmd/gerve/main.go|blame"
  "/commits/HEAD|commits"
  "/commit/$SHA|commit-unified"
  "/commit/$SHA?view=side|commit-side"
  "/refs|refs"
  "/definitely-not-a-page|error-404"
)

shoot() { # path name scheme width
  local log
  log="$OUT/.last-error.log"
  npx -y playwright@1.56.1 screenshot \
    --browser=chromium \
    --viewport-size="${4%%x*},${4##*x}" \
    --color-scheme="$3" \
    --full-page \
    --wait-for-timeout=500 \
    "$BASE$1" "$OUT/$2-$3-w${4%%x}.png" 2> "$log" \
    && echo "  ok $2-$3-w${4%%x}" \
    || { echo "  FAIL $2-$3-w${4%%x} — $(tail -2 "$log" | tr '\n' ' ')"; return 1; }
}

for width in 1280x900 390x844; do
  for scheme in dark light; do
    echo "== $scheme @ $width =="
    for p in "${pages[@]}"; do
      shoot "${p%%|*}" "${p##*|}" "$scheme" "$width"
    done
  done
done
echo "done -> $OUT"

echo "== width audit (mobile shots must be exactly 390px wide) =="
bad=0
for f in "$OUT"/*-w390x844.png; do
  w=$(sips -g pixelWidth "$f" 2>/dev/null | awk '/pixelWidth/{print $2}')
  if [ -n "$w" ] && [ "$w" != "390" ]; then
    echo "  OVERFLOW $(basename "$f") width=$w"
    bad=1
  fi
done
[ "$bad" = 0 ] && echo "  no horizontal overflow" || exit 1
