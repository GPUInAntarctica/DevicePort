# Linux AppImage / Linux AppImage 说明

`dist/DevicePort-x86_64.AppImage` is the recommended way to run DevicePort on a Linux
desktop. It bundles the fully static `deviceport` binary, a launcher menu and an icon.

## Why an AppImage?

`deviceport` is a command-line program. Double-clicking the bare ELF file in a file
manager usually does nothing visible (no terminal window, and the node exits quickly if
no TUN permission is available). The AppImage fixes this:

- The bundled `.desktop` file uses `Terminal=true`, so double-clicking opens a terminal.
- `AppRun` shows a menu: **Start server**, **Start node**, **Show help**, **Quit**.
- Arguments are passed straight through: `./DevicePort-x86_64.AppImage node --server ...`.

## Usage

```bash
chmod +x DevicePort-x86_64.AppImage
./DevicePort-x86_64.AppImage            # interactive menu
./DevicePort-x86_64.AppImage server --listen '[::]:9000' --subnet 10.66.0.0/24
./DevicePort-x86_64.AppImage node --server '[::1]:9000' --name alice
./DevicePort-x86_64.AppImage version
```

The AppImage needs FUSE to mount. If your system has no FUSE (or mounting is blocked),
run it extracted:

```bash
./DevicePort-x86_64.AppImage --appimage-extract-and-run
# or
APPIMAGE_EXTRACT_AND_RUN=1 ./DevicePort-x86_64.AppImage
```

## Build

```bash
# 1. build the static Linux binary
make build

# 2. build the AppImage (appimagetool path can be set with APPIMAGETOOL)
APPIMAGETOOL=/path/to/appimagetool-x86_64.AppImage make appimage
```

The AppDir sources live in `packaging/appimage/AppDir/`:

| Path | Purpose |
|------|---------|
| `AppRun` | launcher + interactive menu |
| `usr/bin/deviceport` | static release binary (copied by the build script) |
| `usr/share/applications/deviceport.desktop` | desktop entry (`Terminal=true`) |
| `usr/share/icons/hicolor/256x256/apps/deviceport.png` | icon |

## 中文

Linux 上推荐用 AppImage。`deviceport` 是命令行程序，直接双击普通 ELF 文件通常不会弹出
终端窗口，看起来像"打不开"。AppImage 的 `.desktop` 设置了 `Terminal=true`，双击会打开
终端并显示菜单（启动服务端 / 启动节点 / 帮助 / 退出），也可以直接带参数运行。
没有 FUSE 的环境可用 `--appimage-extract-and-run`。
