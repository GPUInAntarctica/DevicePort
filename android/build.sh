#!/usr/bin/env bash
# Build the DevicePort Android APK.
#
# Prerequisites:
#   - Android SDK (platform android-35, build-tools), NDK, JDK
#   - gomobile (installed via `go install golang.org/x/mobile/cmd/gomobile@latest`)
#
# The gomobile AAR can be supplied with ANDROID_AAR=/path/to/deviceport.aar.
# Otherwise this script builds it with `gomobile bind`.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
BUILD_TOOLS="${ANDROID_BUILD_TOOLS:-$ANDROID_HOME/build-tools/36.0.0}"
PLATFORM="$ANDROID_HOME/platforms/android-35"
NDK="${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/27.2.12479018}"
VERSION="v0.1.5"
OUT="$ROOT/bin/DevicePort-$VERSION.apk"
BUILD="$HERE/build"

echo "==> DevicePort Android build $VERSION"
mkdir -p "$ROOT/bin"
rm -rf "$BUILD"
mkdir -p "$BUILD/aar" "$BUILD/classes" "$BUILD/dex" "$BUILD/gen" "$BUILD/res"

# ---------------------------------------------------------------- AAR
AAR="${ANDROID_AAR:-$HERE/app/libs/deviceport.aar}"
if [ ! -f "$AAR" ]; then
  echo "==> Building gomobile AAR"
  export ANDROID_HOME ANDROID_NDK_HOME="$NDK"
  (cd "$ROOT" && gomobile bind -androidapi 35 -target=android -o "$HERE/app/libs/deviceport.aar" ./mobile)
  AAR="$HERE/app/libs/deviceport.aar"
fi
(cd "$BUILD/aar" && unzip -oq "$AAR")

# ---------------------------------------------------------------- resources
# aapt2 link generates R.java, which the Java sources need to compile.
echo "==> Linking resources"
cp -r "$HERE/app/src/main/res/." "$BUILD/res/"
"$BUILD_TOOLS/aapt2" compile --dir "$BUILD/res" -o "$BUILD/res.zip"
"$BUILD_TOOLS/aapt2" link \
  -o "$BUILD/base.apk" \
  -I "$PLATFORM/android.jar" \
  --manifest "$HERE/app/src/main/AndroidManifest.xml" \
  -R "$BUILD/res.zip" \
  --java "$BUILD/gen" \
  --auto-add-overlay \
  --min-sdk-version 24 --target-sdk-version 35 \
  --version-code 1 --version-name "$VERSION"

# ---------------------------------------------------------------- Java
echo "==> Compiling Java sources"
javac -source 17 -target 17 \
  -classpath "$PLATFORM/android.jar:$BUILD/aar/classes.jar" \
  -d "$BUILD/classes" \
  $(find "$HERE/app/src/main/java" -name '*.java') \
  $(find "$BUILD/gen" -name 'R.java')

echo "==> Dexing"
"$BUILD_TOOLS/d8" --lib "$PLATFORM/android.jar" \
  --output "$BUILD/dex" \
  $(find "$BUILD/classes" -name '*.class') "$BUILD/aar/classes.jar"

# ---------------------------------------------------------------- package
echo "==> Packaging APK"
cd "$BUILD"
cp dex/classes.dex .
mkdir -p lib
for abi in arm64-v8a x86_64 armeabi-v7a x86; do
  mkdir -p "lib/$abi"
  cp "aar/jni/$abi/libgojni.so" "lib/$abi/"
done
zip -qr base.apk classes.dex lib

"$BUILD_TOOLS/zipalign" -f 4 base.apk aligned.apk

if [ ! -f "$HERE/debug.jks" ]; then
  keytool -genkeypair -keystore "$HERE/debug.jks" -alias androiddebugkey \
    -keyalg RSA -keysize 2048 -validity 10000 \
    -storepass android -keypass android \
    -dname "CN=Android Debug,O=DevicePort,C=US" >/dev/null 2>&1
fi
"$BUILD_TOOLS/apksigner" sign \
  --ks "$HERE/debug.jks" --ks-key-alias androiddebugkey \
  --ks-pass pass:android --key-pass pass:android \
  aligned.apk

cp aligned.apk "$OUT"
echo "==> APK written to $OUT"
"$BUILD_TOOLS/apksigner" verify --print-certs "$OUT" | head -5
