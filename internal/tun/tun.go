// Package tun abstracts the virtual network interface used by the overlay.
//
// Three implementations are provided:
//   - UserspaceTun: an in-memory device used for tests and for platforms where
//     no kernel TUN adapter is available (it is also the Wine test fallback).
//   - LinuxTun (tun_linux.go): a real /dev/net/tun interface.
//   - WintunTun (tun_windows.go): a real Windows adapter via wintun.dll.
//
// An Android VpnService fd can be wrapped with NewFDTun on Linux/Android builds.
package tun

import (
	"errors"
	"io"
	"sync"
)

// Device is a virtual point-to-point IP interface. Read returns the next raw
// IPv4 packet destined for the overlay; Write injects a raw IPv4 packet into
// the local kernel stack.
type Device interface {
	Name() string
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	MTU() int
}

// UserspaceTun is an in-memory Device. Inject() simulates a packet arriving
// from the local OS; packets passed to Write() are queued for delivery back to
// the OS and can be read from Outbound(). This makes the full encryption and
// transport pipeline testable without root privileges or a kernel adapter.
type UserspaceTun struct {
	mu       sync.Mutex
	name     string
	mtu      int
	inbound  chan []byte
	outbound chan []byte
	closed   chan struct{}
	once     sync.Once
}

// NewUserspaceTun creates an in-memory TUN device.
func NewUserspaceTun(name string, mtu int) *UserspaceTun {
	if mtu <= 0 {
		mtu = 1400
	}
	return &UserspaceTun{
		name:     name,
		mtu:      mtu,
		inbound:  make(chan []byte, 256),
		outbound: make(chan []byte, 256),
		closed:   make(chan struct{}),
	}
}

func (t *UserspaceTun) Name() string { return t.name }
func (t *UserspaceTun) MTU() int     { return t.mtu }

func (t *UserspaceTun) Read(b []byte) (int, error) {
	select {
	case p := <-t.inbound:
		return copy(b, p), nil
	case <-t.closed:
		return 0, io.EOF
	}
}

func (t *UserspaceTun) Write(b []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, io.EOF
	default:
	}
	cp := append([]byte(nil), b...)
	select {
	case t.outbound <- cp:
		return len(b), nil
	case <-t.closed:
		return 0, io.EOF
	}
}

// Inject pushes a packet into the device as if it came from the local OS.
func (t *UserspaceTun) Inject(p []byte) error {
	select {
	case <-t.closed:
		return io.EOF
	default:
	}
	cp := append([]byte(nil), p...)
	select {
	case t.inbound <- cp:
		return nil
	case <-t.closed:
		return io.EOF
	}
}

// Outbound returns the channel of packets written to the device by the engine.
func (t *UserspaceTun) Outbound() <-chan []byte { return t.outbound }

func (t *UserspaceTun) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

// ErrNoTun is returned when a real adapter cannot be created on this platform.
var ErrNoTun = errors.New("no native TUN adapter available")
