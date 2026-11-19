//go:build linux

package tun

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

const (
	tunDevice   = "/dev/net/tun"
	tunSetIFF   = 0x400454ca
	flagIFFTUN  = 0x0001
	flagIFFNOPI = 0x1000
	ifreqSize   = 40
)

type ifreq struct {
	Name  [16]byte
	Flags uint16
	_     [ifreqSize - 16 - 2]byte
}

// LinuxTun is a real Linux TUN interface created through /dev/net/tun.
type LinuxTun struct {
	file *os.File
	name string
	mtu  int
}

// NewLinuxTun creates (or opens) a TUN interface named name. A trailing %d in
// the name is replaced by the kernel with a free interface number.
func NewLinuxTun(name string, mtu int) (*LinuxTun, error) {
	if mtu <= 0 {
		mtu = 1400
	}
	f, err := os.OpenFile(tunDevice, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", tunDevice, err)
	}
	var req ifreq
	copy(req.Name[:], name)
	if len(name) >= len(req.Name) {
		req.Name[len(req.Name)-1] = 0
	}
	req.Flags = flagIFFTUN | flagIFFNOPI
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(tunSetIFF), uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		f.Close()
		return nil, fmt.Errorf("ioctl(TUNSETIFF): %w", errno)
	}
	ifName := strings.TrimRight(string(req.Name[:]), "\x00")

	t := &LinuxTun{file: f, name: ifName, mtu: mtu}
	// Configure the interface. These require CAP_NET_ADMIN; failure is not
	// fatal because the caller may configure it out-of-band.
	_ = exec.Command("ip", "link", "set", "dev", ifName, "up").Run()
	_ = exec.Command("ip", "link", "set", "dev", ifName, "mtu", fmt.Sprintf("%d", mtu)).Run()
	return t, nil
}

// SetAddress assigns an IPv4 address (CIDR) to the interface via `ip`.
func (t *LinuxTun) SetAddress(cidr string) error {
	out, err := exec.Command("ip", "addr", "add", cidr, "dev", t.name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip addr add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *LinuxTun) Name() string                { return t.name }
func (t *LinuxTun) MTU() int                    { return t.mtu }
func (t *LinuxTun) Read(b []byte) (int, error)  { return t.file.Read(b) }
func (t *LinuxTun) Write(b []byte) (int, error) { return t.file.Write(b) }
func (t *LinuxTun) Close() error                { return t.file.Close() }
