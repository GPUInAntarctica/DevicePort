// Command deviceport is the single-file DevicePort executable.
//
// Usage:
//
//	deviceport server [options]     start signaling + relay + admin server
//	deviceport node [options]       start the overlay client daemon (default)
//
// Running `deviceport` without a subcommand is equivalent to `deviceport node`.
// Use `deviceport <subcommand> -h` for the full option list.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/GPUInAntarctica/DevicePort/internal/cli"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		// On Windows a double-clicked console program has no visible window and
		// exits immediately. Launch the self-contained desktop mode instead.
		if runtime.GOOS == "windows" {
			self, err := os.Executable()
			if err == nil {
				cli.RunDesktop(self)
				return
			}
		}
		cli.RunNode(nil)
		return
	}
	switch args[0] {
	case "server", "srv":
		cli.RunServer(args[1:])
	case "node", "client":
		cli.RunNode(args[1:])
	case "version", "-V", "--version":
		fmt.Println("DevicePort 1.2.6.5")
	case "help", "-h", "--help":
		usage()
	default:
		// Unknown subcommand: treat as node flags for backward compatibility.
		cli.RunNode(args)
	}
}

func usage() {
	fmt.Print(`DevicePort 1.2.6.5 — IPv6 overlay VPN

Usage:
  deviceport server [options]   signaling, relay and admin API
  deviceport node [options]     overlay client + web GUI (default)
  deviceport version            print version

Quick start:
  deviceport server --listen '[::]:9000' --subnet 10.66.0.0/24
  deviceport node   --server '[::1]:9000' --name alice

Run 'deviceport server -h' or 'deviceport node -h' for all options.
`)
}
