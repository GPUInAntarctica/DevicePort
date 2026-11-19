# Changelog

All notable changes to this project are documented here.

## v0.1.5.5

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

## v0.1.5

- Initial release.
- IPv6 underlay overlay with virtual IPv4 allocation (`10.66.x.y/24`).
- X25519 identities, HKDF-SHA256, AES-256-GCM authenticated frames.
- UDP hole punching with automatic relay fallback.
- TUN devices for Linux and Windows/Wintun, Android VpnService fd, userspace TUN.
- Embedded bilingual (EN/中文) web control panel and HTTP API.
- Electron desktop shell with tray and bilingual menus.
- Android APK build via gomobile AAR + manual APK packaging.
- Cross-platform binaries and end-to-end tests (direct + relay).
