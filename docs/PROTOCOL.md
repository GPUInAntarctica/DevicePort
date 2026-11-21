# Wire protocol (v2, DevicePort v0.1.6)

Two independent formats are used.

## Control messages (TCP, JSON)

Each message is a 4-byte big-endian length followed by a UTF-8 JSON object:

```
[ len:4 ][ {"type":"hello", ...} ]
```

Common fields (all optional except `type`): `id`, `name`, `pubkey` (byte array,
JSON base64), `network`, `port`, `endpoints`, `your_ip`, `prefix`, `peers`,
`peer`, `target`, `to`, `from`, `data` (base64), `message`.

| Type | Direction | Meaning |
|------|-----------|---------|
| `hello` | C→S | announce identity, UDP port, IPv6 endpoints, network |
| `welcome` | S→C | assigned IP/prefix, relay support, current peer list |
| `peer` | S→C | introduction to a peer (id, pubkey, IP, endpoints) |
| `peer_left` | S→C | peer disconnected |
| `connect` | C→S | request introduction to a target node ID |
| `relay` | C→S / S→C | encrypted frame with `to`/`from` and base64 `data` |
| `ping` / `pong` | C→S / S→C | liveness |
| `error` | S→C | human-readable error |

## Data frames (UDP / relay payload)

```
offset  size  field
0       2     magic  = 0x44 0x50 ("DP")
2       1     version = 2
3       1     flags   = bit0: relayed
4       8     sender short ID (first 8 bytes of SHA-256(pubkey))
12      8     sequence number (big endian, monotonic per sender)
20      12    AES-GCM nonce (random per packet)
32      n     AES-GCM ciphertext
```

The 12-byte header is used as GCM additional authenticated data. The plaintext is:

```
[ type:1 ][ payload ]
```

Packet types: `1` = raw IPv4 packet (TUN payload), `2` = punch probe, `3` = keepalive.

Key derivation:

```
shared  = X25519(my_priv, their_pub)
key     = HKDF-SHA256(shared, salt=nil, info="deviceport-v1/overlay-key", len=32)
```

## Compatibility notes

- The `version` byte must be bumped on any incompatible layout change (v2 added the sequence field).
- The 20-byte header including the sequence number is the GCM AAD. Receivers keep a
  64-bit sliding window per peer and drop replays/out-of-window packets.
- Keepalive payloads are `[type=3][8-byte echo token][direction]`. The responder echoes
  the token; the initiator measures RTT locally without synchronized clocks.
- Relay frames set the `relayed` flag at send time; receivers do not treat relayed
  packets as proof of a direct path.
- `welcome` carries `authorized` and the managed `routes` list; `routes` messages update routes live.
- LAN discovery is an optional out-of-band hint: a small JSON announce is sent to
  IPv6 multicast group `ff02::1`, UDP port 9977. It carries the sender's ID, name,
  network name, data-plane UDP port and public key. Receivers combine the observed
  source address with the announced port and feed the result into the normal
  encrypted punch path. It never replaces server authorization.
- Endpoints are IPv6-first `host:port` strings; link-local addresses should include
  a zone and are filtered out by the default endpoint discovery.
