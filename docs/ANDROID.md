# Android guide / Android 指南

## Full VPN app (APK)

`android/` contains a small Android app that:

- requests VPN permission through `VpnService.prepare()`,
- creates the TUN interface with `VpnService.Builder.establish()`,
- passes the file descriptor to the Go engine bundled in a gomobile AAR,
- starts the embedded bilingual Web GUI on `127.0.0.1:9050` and shows it in a WebView.

Build:

```bash
# requires Android SDK (platform 35, build-tools 36), NDK 27, JDK 17+, gomobile
export ANDROID_HOME=$HOME/Android/Sdk
export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/27.2.12479018
bash android/build.sh
# => bin/DevicePort-v0.1.5.apk
adb install -r bin/DevicePort-v0.1.5.apk
```

The build script uses a gomobile AAR (build it with `gomobile bind -androidapi 35
-target=android -o deviceport.aar ./mobile`, or set `ANDROID_AAR=/path/to/aar`).

Because `VpnService` must know the local address before connecting, start the server
with `--subnet 10.66.0.0/24` so the app's `10.66.0.2/24` matches the allocation:

```bash
./dist/deviceport-linux-amd64 server --listen '[::]:9000' --subnet 10.66.0.0/24
```

The app connects to `10.0.2.2:9000` (the host from the emulator's point of view).

## Testing with the Android CLI

```bash
android emulator create medium_phone
android emulator start --headless medium_phone
adb install -r bin/DevicePort-v0.1.5.apk
adb shell am start -n com.deviceport.app/.MainActivity
```

Grant the VPN permission dialog, tap **Start VPN**, and the dashboard appears.
Use the EN/中文 switch in the GUI to change language.

## Daemon-only mode

A statically built Go daemon can also run directly on the device (userspace TUN +
GUI), which is handy for automation:

```bash
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o bin/deviceport-android-arm64 ./cmd/deviceport
adb push bin/deviceport-android-arm64 /data/local/tmp/deviceport
adb reverse tcp:9000 tcp:9000
adb forward tcp:9050 tcp:9050
adb shell '/data/local/tmp/deviceport --server 127.0.0.1:9000 --tun userspace --gui 127.0.0.1:9050 &'
curl -s http://127.0.0.1:9050/api/status
```

For x86_64 emulators use `GOARCH=amd64 CGO_ENABLED=1` with the NDK clang as `CC`.

## 中文

完整 APK 使用 `VpnService` 创建 TUN，并通过 gomobile AAR 调用 Go 引擎；内嵌双语 Web 面板在
WebView 中显示。构建脚本见 `android/build.sh`。服务器需加 `--subnet 10.66.0.0/24` 以匹配应用地址。
也可将 Go 守护进程直接推送到模拟器运行（userspace TUN），便于自动化测试。
