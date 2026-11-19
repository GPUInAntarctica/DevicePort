# Desktop app (Electron) / 桌面应用

`desktop/` is a thin Electron shell around the Go daemon.

## Run in development

```bash
make build                 # produce the single executable in dist/ and bin/
cd desktop
npm install
npm run start
```

The main process:

1. locates the daemon in `bin/` (override with `DP_BIN_DIR`),
2. starts it with `--tun auto --gui 127.0.0.1:9050` (override port with `DP_GUI_PORT`),
3. polls `/api/status` until ready, then loads the GUI in a `BrowserWindow`,
4. provides a tray icon and application menus in both English and Chinese
   (selected from `app.getLocale()`),
5. kills the child process on quit.

## Package installers

```bash
cd desktop
npm run dist          # builds for the host platform
npm run dist:win      # NSIS + portable
npm run dist:linux    # AppImage + deb
```

`electron-builder` bundles `bin/deviceport*` as extra resources. On Windows the
packaged app uses Wintun if installed; otherwise it falls back to userspace TUN.

## 中文

`desktop/` 是 Go 守护进程的 Electron 外壳：启动守护进程、等待 API 就绪、在窗口中加载双语
Web 面板，并提供中英文托盘与菜单。`npm run dist` 可打包 Windows/Linux 安装包。
