//go:build windows

package tun

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	wintun               = syscall.NewLazyDLL("wintun.dll")
	procCreateAdapter    = wintun.NewProc("WintunCreateAdapter")
	procOpenAdapter      = wintun.NewProc("WintunOpenAdapter")
	procCloseAdapter     = wintun.NewProc("WintunCloseAdapter")
	procStartSession     = wintun.NewProc("WintunStartSession")
	procEndSession       = wintun.NewProc("WintunEndSession")
	procReceivePacket    = wintun.NewProc("WintunReceivePacket")
	procReleasePacket    = wintun.NewProc("WintunReleaseReceivePacket")
	procAllocatePacket   = wintun.NewProc("WintunAllocateSendPacket")
	procSendPacket       = wintun.NewProc("WintunSendPacket")
	procGetReadWaitEvent = wintun.NewProc("WintunGetReadWaitEvent")
)

const (
	sessionCapacity = 256 << 10 // 256 KiB ring buffer
	waitObject0     = 0
	waitTimeout     = 1000
	waitFailed      = 0xFFFFFFFF
)

// WintunTun is a real Windows TUN adapter backed by the Wintun driver.
// wintun.dll and the Wintun adapter driver must be installed; see docs/WINDOWS.md.
type WintunTun struct {
	adapter uintptr
	session uintptr
	readEvt uintptr
	name    string
	mtu     int
	mu      sync.Mutex
	closed  bool
}

// NewWintunTun creates (or opens) a Wintun adapter named name.
func NewWintunTun(name string, mtu int) (*WintunTun, error) {
	if err := wintun.Load(); err != nil {
		return nil, fmt.Errorf("load wintun.dll: %w (see docs/WINDOWS.md)", err)
	}
	if mtu <= 0 {
		mtu = 1400
	}
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	tunnel, err := syscall.UTF16PtrFromString("DevicePort")
	if err != nil {
		return nil, err
	}
	adapter, _, callErr := procCreateAdapter.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(tunnel)), 0)
	if adapter == 0 || adapter == waitFailed {
		// Try opening an existing adapter with the same name.
		adapter, _, callErr = procOpenAdapter.Call(uintptr(unsafe.Pointer(n)))
	}
	if adapter == 0 || adapter == waitFailed {
		return nil, fmt.Errorf("WintunCreateAdapter: %w", callErr)
	}
	session, _, callErr := procStartSession.Call(adapter, sessionCapacity)
	if session == 0 || session == waitFailed {
		procCloseAdapter.Call(adapter)
		return nil, fmt.Errorf("WintunStartSession: %w", callErr)
	}
	evt, _, _ := procGetReadWaitEvent.Call(session)
	return &WintunTun{
		adapter: adapter,
		session: session,
		readEvt: evt,
		name:    name,
		mtu:     mtu,
	}, nil
}

// SetAddress configures the adapter's IPv4 address via netsh. This is a
// convenience wrapper; production users may prefer the Wintun LUID APIs.
func (t *WintunTun) SetAddress(cidr string) error {
	ip, _, ok := strings.Cut(cidr, "/")
	if !ok {
		return errors.New("cidr must be address/prefix")
	}
	mask := netmaskFromPrefix(cidr)
	cmd := exec.Command("netsh", "interface", "ipv4", "set", "address",
		fmt.Sprintf("name=%s", t.name), "static", ip, mask, "none")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh set address: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func netmaskFromPrefix(cidr string) string {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return "255.255.255.0"
	}
	var p int
	fmt.Sscanf(parts[1], "%d", &p)
	if p < 0 || p > 32 {
		p = 24
	}
	mask := uint32(0xFFFFFFFF) << (32 - p)
	return fmt.Sprintf("%d.%d.%d.%d", byte(mask>>24), byte(mask>>16), byte(mask>>8), byte(mask))
}

func (t *WintunTun) Name() string { return t.name }
func (t *WintunTun) MTU() int     { return t.mtu }

func (t *WintunTun) Read(b []byte) (int, error) {
	for {
		var size uint32
		ptr, _, _ := procReceivePacket.Call(t.session, uintptr(unsafe.Pointer(&size)))
		if ptr != 0 && ptr != waitFailed && size > 0 {
			src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(size))
			n := copy(b, src)
			procReleasePacket.Call(t.session, ptr)
			return n, nil
		}
		if t.readEvt != 0 {
			syscall.WaitForSingleObject(syscall.Handle(t.readEvt), waitTimeout)
		} else {
			time.Sleep(10 * time.Millisecond)
		}
		t.mu.Lock()
		closed := t.closed
		t.mu.Unlock()
		if closed {
			return 0, errors.New("wintun closed")
		}
	}
}

func (t *WintunTun) Write(b []byte) (int, error) {
	ptr, _, callErr := procAllocatePacket.Call(t.session, uintptr(len(b)))
	if ptr == 0 || ptr == waitFailed {
		return 0, fmt.Errorf("WintunAllocateSendPacket: %w", callErr)
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(b))
	copy(dst, b)
	procSendPacket.Call(t.session, ptr)
	return len(b), nil
}

func (t *WintunTun) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()
	if t.session != 0 {
		procEndSession.Call(t.session)
	}
	if t.adapter != 0 {
		procCloseAdapter.Call(t.adapter)
	}
	return nil
}

var _ Device = (*WintunTun)(nil)
