# Windows guide / Windows 指南

## Real Windows (Wintun)

The Windows client uses [Wintun](https://www.wintun.net/) for its TUN adapter.

1. Download Wintun and place `wintun.dll` next to `deviceport.exe` (or on `PATH`).
2. Install the Wintun driver (follow Wintun's documentation; administrator rights required).
3. Run the client:

```powershell
deviceport-windows-amd64.exe server --listen "[::]:9000"
deviceport.exe --server "[::1]:9000" --name alice --tun wintun
```

The daemon creates an adapter named `dp0`; when the server assigns an address it
runs `netsh interface ipv4 set address name=dp0 static <ip> <mask> none`. You can
also configure the adapter manually and use `--tun userspace` for a no-driver demo.

## Testing with Wine

Wine cannot load kernel drivers, so Wintun is unavailable. Build with `make
build-windows` and run with `--tun auto` (or `--tun userspace`): the daemon detects
the missing driver and falls back to the in-memory TUN. The control plane, key
exchange, hole punching, relay, encryption, HTTP API and bilingual GUI all work.

```bash
export WINEDEBUG=-all WINEDLLOVERRIDES="mscoree,mshtml="
wine dist/deviceport-windows-amd64.exe server --listen '[::1]:9000'
wine dist/deviceport-windows-amd64.exe node --server '[::1]:9000' --name wine1 --gui 127.0.0.1:9050
wine dist/deviceport-windows-amd64.exe node --server '[::1]:9000' --name wine2 --gui 127.0.0.1:9051
curl -s http://127.0.0.1:9050/api/status | python3 -m json.tool
```

Two Wine nodes on the same host establish a direct IPv6 loopback link; packet
routing can be checked with `POST /api/send` (a hex raw IPv4 packet) and by watching
the peer's `rx` counter.

## 中文

真实 Windows 需要安装 Wintun 驱动并将 `wintun.dll` 放在程序目录。Wine 无法加载内核驱动，
因此在 Wine 下请使用 `--tun userspace`（`--tun auto` 会自动回退）。控制面、加密、打洞、
中继、API 和双语 GUI 均可在 Wine 中完整测试。
