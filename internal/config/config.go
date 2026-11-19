// Package config loads and persists DevicePort node configuration.
package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
)

// Config is the on-disk configuration of a node.
type Config struct {
	PrivateKey string   `json:"private_key"` // hex X25519 private key
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Server     string   `json:"server"`     // e.g. "[fd00::1]:9000" or "127.0.0.1:9000"
	Network    string   `json:"network"`    // network/team name
	VirtualIP  string   `json:"virtual_ip"` // last assigned IP (informational)
	ListenPort int      `json:"listen_port"`
	MTU        int      `json:"mtu"`
	GUIAddr    string   `json:"gui_addr"`
	Peers      []string `json:"peers"` // peer IDs to auto-connect
	Path       string   `json:"-"`     // file this config was loaded from
}

// Default returns a config with sensible defaults and a fresh identity.
func Default() (*Config, error) {
	return &Config{
		Name:       "device",
		Server:     "[::1]:9000",
		Network:    "default",
		ListenPort: 0, // ephemeral UDP port
		MTU:        1400,
		GUIAddr:    "127.0.0.1:9050",
	}, nil
}

// Load reads a config from disk. If the file does not exist, a default config
// is created with a fresh identity and written back.
func Load(path string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return createNew(path)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.Path = path
	if c.MTU == 0 {
		c.MTU = 1400
	}
	if c.GUIAddr == "" {
		c.GUIAddr = "127.0.0.1:9050"
	}
	if c.Network == "" {
		c.Network = "default"
	}
	return &c, nil
}

func createNew(path string) (*Config, error) {
	id, privHex, err := func() (string, string, error) {
		ident, err := crypto.GenerateIdentity()
		if err != nil {
			return "", "", err
		}
		return ident.ID(), hex.EncodeToString(ident.PrivateKey()), nil
	}()
	if err != nil {
		return nil, err
	}
	c := &Config{
		PrivateKey: privHex,
		ID:         id,
		Name:       "device",
		Server:     "[::1]:9000",
		Network:    "default",
		ListenPort: 0,
		MTU:        1400,
		GUIAddr:    "127.0.0.1:9050",
		Path:       path,
	}
	if err := c.Save(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save writes the config to disk with restrictive permissions.
func (c *Config) Save() error {
	if c.Path == "" {
		return errors.New("config path is empty")
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil && filepath.Dir(c.Path) != "." {
		return err
	}
	return os.WriteFile(c.Path, b, 0o600)
}

// PrivateBytes decodes the persisted private key.
func (c *Config) PrivateBytes() ([]byte, error) {
	b, err := hex.DecodeString(c.PrivateKey)
	if err != nil || len(b) != 32 {
		return nil, errors.New("invalid private_key in config")
	}
	return b, nil
}
