// Minimal preload bridge. The GUI is served by the Go daemon over localhost,
// so it only needs a safe handle to report the resolved language back to the
// main process for the native menus/tray.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('deviceport', {
  getLocale: () => ipcRenderer.invoke('dp:getLocale'),
  openExternal: (url) => ipcRenderer.invoke('dp:openExternal', url),
});
