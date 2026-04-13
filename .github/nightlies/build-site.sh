#!/usr/bin/env bash
set -euo pipefail

# Replicates cosmos-sdk's build-site.sh — the carry-forward behavior is the bug

CHANNEL="$1"
IS_MAIN="$2"

SITE_DIR="site"
NIGHTLIES_DIR="$SITE_DIR/nightlies"
CHANNEL_DIR="$NIGHTLIES_DIR/$CHANNEL"

mkdir -p "$SITE_DIR"

# ↓ THE CARRY-FORWARD BUG — copies WHATEVER was in existing/ (could be poisoned)
cp -r existing/* "$SITE_DIR" 2>/dev/null || true

mkdir -p "$CHANNEL_DIR"

# Only refreshes the CURRENT channel
cp artifacts/*.tar.gz "$CHANNEL_DIR/" 2>/dev/null || true
cp artifacts/*.sha256 "$CHANNEL_DIR/" 2>/dev/null || true

# Add a simple landing page
cat > "$SITE_DIR/index.html" <<EOF
<!DOCTYPE html>
<html><body>
<h1>Lab Nightlies</h1>
<p>Channel: ${CHANNEL}</p>
<p>Last build: $(date)</p>
<ul>
$(for dir in "$NIGHTLIES_DIR"/*/; do
  [ -d "$dir" ] || continue
  name=$(basename "$dir")
  echo "  <li>${name}</li>"
done)
</ul>
</body></html>
EOF

echo "[VICTIM] Site contents:"
find "$SITE_DIR" -type f
