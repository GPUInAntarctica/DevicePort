//go:build linux || android

package tun

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// NewFDTun wraps an already-open TUN file descriptor. Android's VpnService
// returns such a descriptor from establish(); the Go engine uses it exactly
// like a Linux TUN because Android runs on the Linux kernel.
func NewFDTun(fd int, name string, mtu int) (*FDTun, error) {
	if fd < 0 {
		return nil, errors.New("invalid tun fd")
	}
	// Android/Java closes its ParcelFileDescriptor when the service stops, so
	// we take our own duplicate to keep the interface alive.
	dupfd, err := syscall.Dup(fd)
	if err != nil {
		return nil, fmt.Errorf("dup tun fd: %w", err)
	}
	if mtu <= 0 {
		mtu = 1400
	}
	return &FDTun{
		file: os.NewFile(uintptr(dupfd), name),
		name: name,
		mtu:  mtu,
	}, nil
}

// FDTun is a TUN device backed by a duplicated file descriptor (Android).
type FDTun struct {
	file *os.File
	name string
	mtu  int
}

func (t *FDTun) Name() string                { return t.name }
func (t *FDTun) MTU() int                    { return t.mtu }
func (t *FDTun) Read(b []byte) (int, error)  { return t.file.Read(b) }
func (t *FDTun) Write(b []byte) (int, error) { return t.file.Write(b) }
func (t *FDTun) Close() error                { return t.file.Close() }
