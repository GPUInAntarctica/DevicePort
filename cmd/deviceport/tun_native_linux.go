//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

func openNativeTUN(mode, name string, mtu int) (tun.Device, func(string, string) error, error) {
	userspace := func() (tun.Device, func(string, string) error, error) {
		return tun.NewUserspaceTun(name, mtu), func(ip, prefix string) error { return nil }, nil
	}
	switch mode {
	case "userspace":
		return userspace()
	case "linux", "auto":
		d, err := tun.NewLinuxTun(name, mtu)
		if err == nil {
			configure := func(ip, prefix string) error {
				bits := "24"
				if _, p, ok := strings.Cut(prefix, "/"); ok {
					bits = p
				}
				return d.SetAddress(ip + "/" + bits)
			}
			return d, configure, nil
		}
		if mode == "linux" {
			return nil, nil, err
		}
		fmt.Printf("native TUN unavailable (%v) — falling back to userspace mode\n", err)
		return userspace()
	default:
		return nil, nil, fmt.Errorf("unknown TUN mode %q", mode)
	}
}
