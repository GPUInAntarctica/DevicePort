//go:build windows || plan9 || js || wasm

package node

import "net"

// enableMulticastLoopback is a no-op on platforms where Go's net package
// already enables IPv6 multicast loopback by default.
func enableMulticastLoopback(_ *net.UDPConn) {}
