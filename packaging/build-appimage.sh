#!/usr/bin/env bash
# Build DevicePort-x86_64.AppImage from the static Linux release binary.
#
# Strategy: reuse the AppImage runtime from the previously committed AppImage.
# A type-2 AppImage is an ELF runtime followed by a SquashFS payload; the payload
# offset is computed from the ELF section headers. This avoids needing
# appimagetool (and FUSE) to rebuild the bundle.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
BIN="$ROOT/dist/deviceport-linux-amd64"
APPDIR="$HERE/appimage/AppDir"
OUT="$ROOT/dist/DevicePort-x86_64.AppImage"
BASE="${APPIMAGE_BASE:-$OUT}"

if [ ! -x "$BIN" ]; then
  echo "Missing $BIN - run 'make build' first" >&2
  exit 1
fi
if [ ! -f "$BASE" ]; then
  echo "Missing base AppImage $BASE (a previous AppImage must exist in dist/)" >&2
  exit 1
fi

# Compute the SquashFS offset from the base AppImage's ELF headers.
OFFSET="$(python3 - "$BASE" <<'PY'
import struct, sys
d = open(sys.argv[1], 'rb').read()
shoff = struct.unpack_from('<Q', d, 0x28)[0]
shentsize = struct.unpack_from('<H', d, 0x3a)[0]
shnum = struct.unpack_from('<H', d, 0x3c)[0]
print(shoff + shentsize * shnum)
PY
)"
if [ "$OFFSET" -le 0 ] || [ "$OFFSET" -ge "$(stat -c %s "$BASE")" ]; then
  echo "Invalid AppImage payload offset $OFFSET" >&2
  exit 1
fi

WORK="$(mktemp -d /tmp/dp-appimage.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
UNPACK="$WORK/AppDir"
unsquashfs -o "$OFFSET" -d "$UNPACK" "$BASE" >/dev/null

# Refresh the payload contents.
cp "$BIN" "$UNPACK/usr/bin/deviceport"
chmod +x "$UNPACK/usr/bin/deviceport"
cp "$APPDIR/AppRun" "$UNPACK/AppRun"
cp "$APPDIR/deviceport.desktop" "$UNPACK/deviceport.desktop"
cp "$APPDIR/deviceport.png" "$UNPACK/deviceport.png"
cp "$APPDIR/usr/share/applications/deviceport.desktop" \
   "$UNPACK/usr/share/applications/deviceport.desktop"
chmod +x "$UNPACK/AppRun"

mksquashfs "$UNPACK" "$WORK/payload.sqfs" -noappend -comp gzip >/dev/null
python3 - "$BASE" "$WORK/payload.sqfs" "$OFFSET" "$OUT" <<'PY'
import sys
base, sqfs, off, out = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
d = open(base, 'rb').read()
s = open(sqfs, 'rb').read()
open(out, 'wb').write(d[:off] + s)
PY
chmod +x "$OUT"
ls -lh "$OUT"
echo "==> Done: $OUT"
