//go:build !linux && !windows

package main

import (
	"fmt"

	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

func openNativeTUN(mode, name string, mtu int) (tun.Device, func(string, string) error, error) {
	if mode == "userspace" || mode == "auto" {
		return tun.NewUserspaceTun(name, mtu), func(string, string) error { return nil }, nil
	}
	return nil, nil, fmt.Errorf("native TUN mode %q is not supported on this platform (use userspace)", mode)
}
