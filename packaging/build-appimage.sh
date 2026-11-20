#!/usr/bin/env bash
# Build DevicePort-x86_64.AppImage from the static Linux release binary.
#
# Requires: appimagetool (path via APPIMAGETOOL, default /tmp/appimagetool-x86_64.AppImage)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
APPIMAGETOOL="${APPIMAGETOOL:-/tmp/appimagetool-x86_64.AppImage}"
BIN="$ROOT/dist/deviceport-linux-amd64"
APPDIR="$HERE/appimage/AppDir"
OUT="$ROOT/dist/DevicePort-x86_64.AppImage"

if [ ! -x "$BIN" ]; then
  echo "Missing $BIN - run 'make build' first" >&2
  exit 1
fi

# Refresh the bundled binary and launcher.
cp "$BIN" "$APPDIR/usr/bin/deviceport"
chmod +x "$APPDIR/usr/bin/deviceport" "$APPDIR/AppRun"
# appimagetool looks for the desktop file and icon at the AppDir root.
cp "$APPDIR/usr/share/applications/deviceport.desktop" "$APPDIR/deviceport.desktop"
cp "$APPDIR/usr/share/icons/hicolor/256x256/apps/deviceport.png" "$APPDIR/deviceport.png"

# appimagetool needs FUSE; extract-and-run works on hosts without /dev/fuse.
if "$APPIMAGETOOL" --version >/dev/null 2>&1; then
  RUNNER="$APPIMAGETOOL"
else
  echo "==> Extracting appimagetool (FUSE unavailable)"
  rm -rf /tmp/appimagetool-extract /tmp/squashfs-root
  (cd /tmp && "$APPIMAGETOOL" --appimage-extract >/dev/null)
  mv /tmp/squashfs-root /tmp/appimagetool-extract
  RUNNER="/tmp/appimagetool-extract/AppRun"
fi

echo "==> Building $(basename "$OUT")"
ARCH=x86_64 "$RUNNER" "$APPDIR" "$OUT" || \
  ARCH=x86_64 APPIMAGE_EXTRACT_AND_RUN=1 "$RUNNER" "$APPDIR" "$OUT"

chmod +x "$OUT"
ls -lh "$OUT"
echo "==> Done: $OUT"
