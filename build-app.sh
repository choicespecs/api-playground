#!/bin/bash
# build-app.sh — builds "API Playground.app" for macOS.
# Run:  chmod +x build-app.sh && ./build-app.sh
# Then drag "API Playground.app" to /Applications.
set -e

APP_NAME="API Playground"
BUNDLE="${APP_NAME}.app"
BINARY="api-playground"

echo "🔨  Building Go binary (with CGO for native window)…"
CGO_ENABLED=1 go build -ldflags="-s -w" -o "$BINARY" .

echo "📦  Assembling .app bundle…"
rm -rf "$BUNDLE"
mkdir -p "$BUNDLE/Contents/MacOS"
mkdir -p "$BUNDLE/Contents/Resources"

# ── Binary ─────────────────────────────────────────────────────────────────
cp "$BINARY" "$BUNDLE/Contents/MacOS/$BINARY"

# ── Templates ──────────────────────────────────────────────────────────────
cp -r templates "$BUNDLE/Contents/Resources/templates"

# ── Launcher script ────────────────────────────────────────────────────────
# Sets up user-data dir, syncs templates, then hands off to the binary
# which opens its own native window — no browser involved.
cat > "$BUNDLE/Contents/MacOS/launcher" << 'LAUNCHER'
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
RESOURCES="$DIR/../Resources"
DATA="$HOME/Library/Application Support/APIPlayground"

# Ensure user-data directory exists
mkdir -p "$DATA"

# Always sync templates from the bundle so updates are picked up on relaunch
rm -rf "$DATA/templates"
cp -r "$RESOURCES/templates" "$DATA/templates"

# Run from the data dir so JSON files (history, vars, collections) land there
cd "$DATA"
exec "$DIR/api-playground"
LAUNCHER

chmod +x "$BUNDLE/Contents/MacOS/launcher"

# ── Info.plist ──────────────────────────────────────────────────────────────
cat > "$BUNDLE/Contents/Info.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
    "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>launcher</string>
    <key>CFBundleIdentifier</key>
    <string>com.apiplayground.app</string>
    <key>CFBundleName</key>
    <string>API Playground</string>
    <key>CFBundleDisplayName</key>
    <string>API Playground</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0</string>
    <key>CFBundleVersion</key>
    <string>1</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.15</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

echo ""
echo "✅  Built: ${BUNDLE}"
echo ""
echo "Next steps:"
echo "  • Test now:    open '${BUNDLE}'"
echo "  • Install:     cp -r '${BUNDLE}' /Applications/"
echo ""
echo "Your data (history, variables, collections) is stored in:"
echo "  ~/Library/Application Support/APIPlayground/"
