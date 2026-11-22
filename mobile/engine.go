// Package mobile is the gomobile-compatible facade used by the Android app.
//
// It deliberately exposes only primitive types (strings, ints, errors) so it
// can be bound to an AAR with `gomobile bind`. The Android VpnService creates
// the TUN interface and passes its file descriptor to NewEngine; on desktop
// the same engine is used with a userspace TUN for tests.
package mobile

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/GPUInAntarctica/DevicePort/internal/api"
	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/node"
	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

// Version is the DevicePort release string.
func Version() string { return "1.2.6.5" }

// NewIdentity generates a fresh X25519 private key and returns it as hex.
func NewIdentity() (string, error) {
	id, err := crypto.GenerateIdentity()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(id.PrivateKey()), nil
}

// NodeID derives the public node ID from a hex private key.
func NodeID(privateHex string) (string, error) {
	b, err := hex.DecodeString(privateHex)
	if err != nil || len(b) != crypto.PrivateKeySize {
		return "", errors.New("private key must be 32 hex-encoded bytes")
	}
	id, err := crypto.FromPrivate(b)
	if err != nil {
		return "", err
	}
	return id.ID(), nil
}

// engineConfig is the JSON accepted by NewEngine.
type engineConfig struct {
	Server       string   `json:"server"`
	Network      string   `json:"network"`
	Name         string   `json:"name"`
	PrivateKey   string   `json:"private_key"`
	MTU          int      `json:"mtu"`
	MaxDirect    int      `json:"max_direct"`
	LANDiscovery *bool    `json:"lan_discovery"`
	IPv6         []string `json:"ipv6"`
}

// Engine wraps a node.Node for the mobile binding.
type Engine struct {
	ident   *crypto.Identity
	dev     tun.Device
	node    *node.Node
	privHex string
}

// NewEngine creates an engine. If tunFD >= 0 it is wrapped as an Android VPN
// TUN interface; otherwise an in-memory userspace TUN is used. configJSON is
// documented in engineConfig.
func NewEngine(configJSON string, tunFD int) (*Engine, error) {
	var cfg engineConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("bad config: %w", err)
	}
	if cfg.PrivateKey == "" {
		priv, err := NewIdentity()
		if err != nil {
			return nil, err
		}
		cfg.PrivateKey = priv
	}
	b, err := hex.DecodeString(cfg.PrivateKey)
	if err != nil || len(b) != crypto.PrivateKeySize {
		return nil, errors.New("private_key must be 32 hex-encoded bytes")
	}
	ident, err := crypto.FromPrivate(b)
	if err != nil {
		return nil, err
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1400
	}
	var dev tun.Device
	if tunFD >= 0 {
		dev, err = tun.NewFDTun(tunFD, "dp0", cfg.MTU)
	} else {
		dev = tun.NewUserspaceTun("dp0", cfg.MTU)
	}
	if err != nil {
		return nil, err
	}
	lanDiscovery := true
	if cfg.LANDiscovery != nil {
		lanDiscovery = *cfg.LANDiscovery
	}
	n, err := node.New(ident, dev, node.Options{
		Server: cfg.Server, Network: cfg.Network, Name: cfg.Name,
		AutoConnect: true, MaxDirect: cfg.MaxDirect,
		LANDiscovery: lanDiscovery,
		DisplayIPv6:  cfg.IPv6,
	})
	if err != nil {
		dev.Close()
		return nil, err
	}
	return &Engine{ident: ident, dev: dev, node: n, privHex: cfg.PrivateKey}, nil
}

// Start connects to the signaling server and starts the data plane.
func (e *Engine) Start() error { return e.node.Start() }

// Status returns a JSON snapshot of the node status.
func (e *Engine) Status() string {
	b, err := json.Marshal(e.node.Status())
	if err != nil {
		return "{}"
	}
	return string(b)
}

// StartGUI serves the embedded bilingual web GUI on addr (e.g. 127.0.0.1:9050).
func (e *Engine) StartGUI(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() { _ = http.Serve(ln, api.New(e.node).Handler()) }()
	return nil
}

// ConnectPeer asks the server to introduce this node to target.
func (e *Engine) ConnectPeer(target string) error { return e.node.ConnectPeer(target) }

// InjectPacket injects a raw IPv4 packet when using the userspace TUN.
func (e *Engine) InjectPacket(pkt []byte) error { return e.node.InjectPacket(pkt) }

// PrivateKey returns this engine's hex private key for persistence.
func (e *Engine) PrivateKey() string { return e.privHex }

// ID returns the node ID.
func (e *Engine) ID() string { return e.ident.ID() }

// Close stops the engine.
func (e *Engine) Close() { _ = e.node.Close() }
