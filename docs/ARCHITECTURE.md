# Architecture / 架构

## Overview / 概览

DevicePort has three logical layers:

1. **Control plane** — a TCP connection from each node to the signaling server.
   It carries `hello`, `welcome`, `peer`, `connect`, `relay`, `ping`/`pong`.
2. **Data plane** — encrypted UDP datagrams exchanged directly between peers over
   the IPv6 underlay. When no direct path exists, the same frames are wrapped in
   control-plane `relay` messages and forwarded by the server.
3. **TUN plane** — raw IPv4 packets read from / written to a virtual interface.
   The engine maps destination IPv4 addresses to peers and forwards packets.

DevicePort 分为三层：控制面（节点到服务器的 TCP 连接）、数据面（节点间 IPv6 UDP 加密数据报，
必要时经服务器中继）、TUN 层（虚拟网卡的 IPv4 原始报文收发与路由）。

## Components / 组件

| Package | Responsibility |
|---------|----------------|
| `cmd/deviceport` | single executable; dispatches `server` / `node` subcommands |
| `internal/cli` | subcommand implementations and platform TUN selection |
| `internal/server` | IP allocation, authorization, managed routes, introductions, blind relay, admin API |
| `internal/node` | peer state, bounded punch loops, replay window, RTT keepalives, IPv6 LAN discovery (`discovery.go`), encryption, TUN + route forwarding |
| `internal/crypto` | X25519 identity, ECDH + HKDF, AES-256-GCM, random nonces |
| `internal/protocol` | control envelopes and binary frame layout |
| `internal/tun` | `Device` interface; Linux, Wintun, FD (Android), userspace implementations |
| `internal/api` | embedded web GUI + local JSON API |
| `mobile` | gomobile-safe facade (`NewEngine`, `Start`, `Status`, `StartGUI`, …) |

## Connection lifecycle / 连接流程

1. Node connects to the server, sends `hello` with its public key, UDP port and
   discovered IPv6 endpoints.
2. Server assigns a stable IPv4 address and replies with `welcome`. If the network
   requires authorization, the node is held in a pending state: it sees no peers and
   displays an "awaiting authorization" banner until an admin approves it.
3. For every learned peer, both nodes derive the same symmetric key (X25519 ECDH +
   HKDF). Only the closest `--max-direct` peers (XOR distance, default 4) are punched;
   the rest communicate via the relay. This avoids O(n²) punch traffic.
   Managed routes from the server are installed and matched by destination IP.
4. In parallel, LAN discovery announces the node on IPv6 multicast `ff02::1:9977`
   every 2 s. Peers on the same link are introduced immediately and punched using
   the announced data-plane port, so local nodes can connect even if they use
   different signaling servers. Disable with `--no-discovery`. Announcements are
   only hints — the encrypted data plane still requires the peer's public key.
4. The first valid direct packet from a peer installs the direct path (`activeEP`).
   Data packets then flow peer-to-peer.
5. If no direct packet arrives within the punch burst, data is sent via the server's
   relay. A periodic retry keeps trying to upgrade relay → direct.
6. Keepalives run every 5 s; stale direct paths (>60 s without traffic) are
   demoted and re-punched. Control pings run every 20 s.

## Security properties / 安全特性

- The relay server sees only ciphertext; it holds no long-term keys.
- Each packet uses a fresh 96-bit random nonce with AES-GCM.
- The 12-byte frame header (magic, version, flags, sender ID) is authenticated as
  GCM additional data, preventing metadata tampering.
- Peer identity is the fingerprint of the X25519 public key, so the server cannot
  impersonate a peer without detection (out-of-band ID verification is recommended).
- Known limitations: no forward secrecy (static keys), no replay protection, no
  global key revocation. These are acceptable for 1.2.6.5 but must be addressed before
  production use.
