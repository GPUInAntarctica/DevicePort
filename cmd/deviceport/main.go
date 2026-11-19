// Command deviceport is the DevicePort client daemon. It creates a virtual
// network interface, connects to the signaling server over IPv6, establishes
// encrypted peer-to-peer links with NAT hole punching (relay fallback), and
// serves a local web GUI for monitoring and control.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/GPUInAntarctica/DevicePort/internal/api"
	"github.com/GPUInAntarctica/DevicePort/internal/config"
	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/node"
	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "configuration file")
	serverAddr := flag.String("server", "", "override signaling server address")
	name := flag.String("name", "", "override device name")
	network := flag.String("network", "", "override network name")
	tunMode := flag.String("tun", "auto", "TUN mode: auto, linux, wintun, userspace")
	guiAddr := flag.String("gui", "", "override GUI listen address (empty disables GUI)")
	connect := flag.String("connect", "", "connect to this peer ID after startup")
	daemon := flag.Bool("daemon", false, "run without the web GUI")
	flag.Parse()

	logger := log.New(os.Stderr, "dp: ", log.LstdFlags)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}
	if *serverAddr != "" {
		cfg.Server = *serverAddr
	}
	if *name != "" {
		cfg.Name = *name
	}
	if *network != "" {
		cfg.Network = *network
	}
	if *guiAddr != "" {
		cfg.GUIAddr = *guiAddr
	}

	priv, err := cfg.PrivateBytes()
	if err != nil {
		logger.Fatalf("load identity: %v", err)
	}
	ident, err := crypto.FromPrivate(priv)
	if err != nil {
		logger.Fatalf("load identity: %v", err)
	}

	dev, configure, err := openTUN(*tunMode, "dp0", cfg.MTU)
	if err != nil {
		logger.Fatalf("open TUN: %v", err)
	}
	defer dev.Close()

	n, err := node.New(ident, dev, node.Options{
		Server:      cfg.Server,
		Network:     cfg.Network,
		Name:        cfg.Name,
		Logger:      logger,
		AutoConnect: true,
	})
	if err != nil {
		logger.Fatalf("create node: %v", err)
	}
	n.OnAddress = configure
	defer n.Close()

	if err := n.Start(); err != nil {
		logger.Fatalf("start node: %v", err)
	}
	if *connect != "" {
		if err := n.ConnectPeer(*connect); err != nil {
			logger.Printf("connect to %s: %v", *connect, err)
		}
	}

	if !*daemon && cfg.GUIAddr != "" {
		srv := &http.Server{Addr: cfg.GUIAddr, Handler: api.New(n).Handler()}
		go func() {
			logger.Printf("web GUI at http://%s", cfg.GUIAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Printf("GUI server: %v", err)
			}
		}()
	}

	logger.Printf("node %s online — virtual IP assigned by %s", ident.ID(), cfg.Server)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println()
	logger.Println("shutting down")
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "deviceport.json"
	}
	return filepath.Join(dir, "deviceport", "config.json")
}

// openTUN is implemented per-platform in tun_native_*.go.
func openTUN(mode, name string, mtu int) (tun.Device, func(ip, prefix string) error, error) {
	return openNativeTUN(mode, name, mtu)
}
