//go:build windows

package main

import (
	"fmt"

	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

func openNativeTUN(mode, name string, mtu int) (tun.Device, func(string, string) error, error) {
	userspace := func() (tun.Device, func(string, string) error, error) {
		return tun.NewUserspaceTun(name, mtu), func(ip, prefix string) error { return nil }, nil
	}
	switch mode {
	case "userspace":
		return userspace()
	case "wintun", "auto":
		d, err := tun.NewWintunTun(name, mtu)
		if err == nil {
			configure := func(ip, prefix string) error {
				return d.SetAddress(ip + "/24")
			}
			return d, configure, nil
		}
		if mode == "wintun" {
			return nil, nil, err
		}
		fmt.Printf("Wintun unavailable (%v) — falling back to userspace mode\n", err)
		return userspace()
	default:
		return nil, nil, fmt.Errorf("unknown TUN mode %q", mode)
	}
}
