//go:build linux || android || darwin || freebsd || netbsd || openbsd

package node

import (
	"net"
	"syscall"
)

// enableMulticastLoopback makes multicast datagrams sent by this host visible
// to other sockets on the same host. Some stacks default this off, which
// otherwise breaks discovery between local nodes and the Android emulator.
func enableMulticastLoopback(c *net.UDPConn) {
	raw, err := c.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_LOOP, 1)
	})
}
