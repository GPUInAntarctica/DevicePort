# Changelog

All notable changes to this project are documented here.

## 1.2.6.5

- Web console: all four sidebar items (Overview / Networks / Members / Settings) are now real, clickable pages with an active state and a remembered selection.
- Networks page: join or create a network by entering an IPv6 controller address (`[2001:db8::1]:9000`, `fe80::1%wlan0`, ...). The node keeps its identity and TUN device and reconnects immediately; saved networks can be switched back to or removed.
- IPv6 addresses are listed with one-click copy buttons, and the console shows member/direct/online counters and clearer empty states.
- Version updated to 1.2.6.5.

## v0.1.6

- Android no longer hardcodes `10.66.0.2`: it probes the server-assigned IP with a temporary userspace engine, then establishes `VpnService` with the real address and route. Fixes IP conflicts when the Android node is not the first member.
- IPv6 multicast LAN discovery (`ff02::1`, port 9977): peers on the same link are introduced directly without waiting for the signaling server; disable with `--no-discovery`.
- The console now shows the node's underlay IPv6 addresses and LAN-discovery status.
- Server reconnect race fixed: a late disconnect from an old connection no longer removes the newly connected client with the same identity.
- Windows desktop mode: double-clicking the .exe starts a local network and opens the control panel in the browser; CLI arguments still work.
- Linux AppImage: registered launcher opens the control panel in a browser on double-click (`packaging/install-linux.sh`); unregistered AppImages pass arguments through a terminal menu.

## v0.1.5

- Single-file executable for Linux and Windows with `server` / `node` subcommands.
- Per-network member authorization (admin API + `deviceportctl`), with a waiting banner in the console.
- 64-bit sequence numbers in authenticated frame headers and a sliding replay window.
- Bounded direct links (`--max-direct`, default 4) with XOR-distance selection; no full mesh by default.
- Managed site-to-site routes pushed by the server, with gateway forwarding in the data plane.
- Real peer status: online/offline, RTT from echoed keepalives, last-seen and dropped counters.
- Stable per-member IP allocation.
- Android settings page for server, network and device name.
- ZeroTier-style dark console UI.
- Ready-to-run Linux, Windows and APK assets tracked in `dist/`.

## v0.1

- Initial release.
- IPv6 underlay overlay with virtual IPv4 allocation (`10.66.x.y/24`).
- X25519 identities, HKDF-SHA256, AES-256-GCM authenticated frames.
- UDP hole punching with automatic relay fallback.
- TUN devices for Linux and Windows/Wintun, Android VpnService fd, userspace TUN.
- Embedded bilingual (EN/中文) web control panel and HTTP API.
- Electron desktop shell with tray and bilingual menus.
- Android APK build via gomobile AAR + manual APK packaging.
- Cross-platform binaries and end-to-end tests (direct + relay).
