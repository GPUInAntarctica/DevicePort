// DevicePort Electron shell.
//
// This is a thin native wrapper around the Go `deviceport` daemon and its
// embedded Web GUI. It:
//   1. starts the daemon with a localhost GUI port (userspace/native TUN auto),
//   2. waits for the API to become ready,
//   3. shows the UI in a BrowserWindow with a system tray and bilingual menus,
//   4. cleans up the child process on quit.
//
// The Go binaries are expected in ./bin (bundled by electron-builder) or in
// ../bin after running `make build`. Override with DP_BIN_DIR.
const { app, BrowserWindow, Tray, Menu, shell, nativeImage, ipcMain } = require('electron');
const { spawn } = require('child_process');
const http = require('http');
const path = require('path');
const fs = require('fs');

// ---------------------------------------------------------------- i18n
const STR = {
  en: {
    show: 'Show DevicePort', quit: 'Quit DevicePort', hide: 'Hide',
    status: 'Status', openBrowser: 'Open in browser', about: 'About',
    aboutText: 'DevicePort — IPv6 overlay VPN with virtual IPv4, NAT traversal, relay fallback and end-to-end encryption.',
    daemonStart: 'Starting DevicePort daemon…', daemonExit: 'DevicePort daemon exited',
    appMenu: 'App', help: 'Help', relaunch: 'Relaunch daemon',
  },
  zh: {
    show: '显示 DevicePort', quit: '退出 DevicePort', hide: '隐藏',
    status: '状态', openBrowser: '在浏览器中打开', about: '关于',
    aboutText: 'DevicePort — 基于 IPv6 的覆盖网络 VPN，提供虚拟 IPv4、NAT 穿透、中继回退与端到端加密。',
    daemonStart: '正在启动 DevicePort 守护进程…', daemonExit: 'DevicePort 守护进程已退出',
    appMenu: '应用', help: '帮助', relaunch: '重启守护进程',
  },
};
let lang = 'en';
let daemon = null;
let tray = null;
let win = null;
const guiPort = Number(process.env.DP_GUI_PORT || 9050);
const guiURL = `http://127.0.0.1:${guiPort}/`;
const t = (k) => (STR[lang] && STR[lang][k]) || STR.en[k] || k;

// ---------------------------------------------------------------- daemon
function binDir() {
  if (process.env.DP_BIN_DIR) return process.env.DP_BIN_DIR;
  const bundled = path.join(process.resourcesPath || __dirname, 'bin');
  if (fs.existsSync(bundled)) return bundled;
  return path.join(__dirname, '..', 'bin');
}

function startDaemon() {
  const name = process.platform === 'win32' ? 'deviceport.exe' : 'deviceport';
  const bin = path.join(binDir(), name);
  if (!fs.existsSync(bin)) {
    showError(`Cannot find daemon binary: ${bin}\nRun "make build" first.`);
    return;
  }
  const args = ['--tun', 'auto', '--gui', `127.0.0.1:${guiPort}`];
  const cfg = process.env.DP_CONFIG;
  if (cfg) args.push('--config', cfg);
  daemon = spawn(bin, args, { stdio: ['ignore', 'pipe', 'pipe'] });
  daemon.stdout.on('data', (b) => process.stdout.write(`[dp] ${b}`));
  daemon.stderr.on('data', (b) => process.stderr.write(`[dp] ${b}`));
  daemon.on('exit', (code) => {
    if (!app.isQuitting) console.log(t('daemonExit'), code);
    daemon = null;
  });
}

function waitForAPI(retries, cb) {
  const req = http.get(guiURL + 'api/status', (res) => {
    res.resume();
    if (res.statusCode === 200) return cb(true);
    retry();
  });
  req.on('error', retry);
  req.setTimeout(500, () => req.destroy());
  function retry() {
    if (retries <= 0) return cb(false);
    setTimeout(() => waitForAPI(retries - 1, cb), 200);
  }
}

// ---------------------------------------------------------------- window
function createWindow() {
  win = new BrowserWindow({
    width: 1120, height: 780, minWidth: 760, minHeight: 560,
    title: 'DevicePort', icon: iconPath(), show: false,
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false },
  });
  win.on('close', (e) => {
    if (!app.isQuitting) { e.preventDefault(); win.hide(); }
  });
  buildMenus();
  waitForAPI(60, (ok) => {
    if (!ok) showError('DevicePort API did not become ready.\nCheck that the daemon can reach the signaling server.');
    win.loadURL(guiURL);
    win.show();
  });
}

function iconPath() {
  const p = path.join(__dirname, 'icons', 'icon.png');
  return fs.existsSync(p) ? p : undefined;
}

function buildMenus() {
  const template = [
    {
      label: t('appMenu'),
      submenu: [
        { label: t('show'), click: () => { win.show(); win.focus(); } },
        { label: t('relaunch'), click: () => { if (daemon) daemon.kill(); setTimeout(startDaemon, 300); } },
        { type: 'separator' },
        { label: t('quit'), click: () => { app.isQuitting = true; app.quit(); } },
      ],
    },
    {
      label: t('help'),
      submenu: [
        { label: t('openBrowser'), click: () => shell.openExternal(guiURL) },
        { label: t('about'), click: () => {
          const { dialog } = require('electron');
          dialog.showMessageBox({ title: 'DevicePort', message: 'DevicePort', detail: t('aboutText') });
        } },
      ],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));

  if (tray) tray.destroy();
  const img = iconPath() ? nativeImage.createFromPath(iconPath()).resize({ width: 16, height: 16 }) : nativeImage.createEmpty();
  tray = new Tray(img);
  tray.setToolTip('DevicePort');
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: t('show'), click: () => { win.show(); win.focus(); } },
    { label: t('status'), click: () => { win.show(); win.loadURL(guiURL); } },
    { type: 'separator' },
    { label: t('quit'), click: () => { app.isQuitting = true; app.quit(); } },
  ]));
  tray.on('double-click', () => { win.show(); win.focus(); });
}

function showError(msg) {
  const { dialog } = require('electron');
  dialog.showErrorBox('DevicePort', msg);
}

// ---------------------------------------------------------------- IPC
ipcMain.handle('dp:getLocale', () => lang);
ipcMain.handle('dp:openExternal', (_e, url) => shell.openExternal(url));

// ---------------------------------------------------------------- lifecycle
app.whenReady().then(() => {
  lang = app.getLocale().toLowerCase().startsWith('zh') ? 'zh' : 'en';
  startDaemon();
  createWindow();
  app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow(); });
});

app.on('before-quit', () => { app.isQuitting = true; if (daemon) daemon.kill(); });
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
