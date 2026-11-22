package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/GPUInAntarctica/DevicePort/internal/node"
)

// Network is a saved controller/network combination that the GUI can join.
type Network struct {
	Name    string `json:"name"`
	IPv6    string `json:"ipv6"`   // underlay address, including optional %zone
	Server  string `json:"server"` // "[ipv6]:port"
	Current bool   `json:"current"`
}

type networkStore struct {
	mu       sync.Mutex
	path     string
	networks []Network
}

func newNetworkStore() *networkStore {
	s := &networkStore{}
	if dir, err := os.UserConfigDir(); err == nil {
		s.path = filepath.Join(dir, "deviceport", "networks.json")
	}
	s.load()
	return s
}

func (s *networkStore) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var out []Network
	if json.Unmarshal(b, &out) == nil {
		s.networks = out
	}
}

func (s *networkStore) save() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(s.networks, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
	_ = os.WriteFile(s.path, b, 0o600)
}

func (s *networkStore) add(n Network) {
	for i := range s.networks {
		if strings.EqualFold(s.networks[i].Name, n.Name) {
			s.networks[i] = n
			s.save()
			return
		}
	}
	s.networks = append(s.networks, n)
	s.save()
}

func (s *networkStore) remove(name string) bool {
	for i := range s.networks {
		if strings.EqualFold(s.networks[i].Name, name) {
			s.networks = append(s.networks[:i], s.networks[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

func (s *networkStore) find(name string) (Network, bool) {
	for _, n := range s.networks {
		if strings.EqualFold(n.Name, name) {
			return n, true
		}
	}
	return Network{}, false
}

// normalizeController accepts an IPv6 address (optionally with a %zone), or a
// bracketed IPv6 host:port, and returns the address plus a dialable server.
func normalizeController(input, defaultPort string) (ipv6, server string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errors.New("IPv6 地址不能为空 / IPv6 address is required")
	}
	host, port := input, defaultPort
	if strings.HasPrefix(input, "[") {
		end := strings.LastIndex(input, "]")
		if end < 0 {
			return "", "", errors.New("invalid bracketed IPv6 address")
		}
		host = input[1:end]
		rest := strings.TrimSpace(input[end+1:])
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", "", errors.New("invalid port")
			}
			port = strings.TrimPrefix(rest, ":")
		}
	} else if strings.Contains(input, ".") && !strings.Contains(input, ":") {
		return "", "", errors.New("请输入 IPv6 地址 / enter an IPv6 address")
	}
	// A zone is not part of the address itself but is required to dial it.
	zone := ""
	if i := strings.IndexByte(host, '%'); i >= 0 {
		zone = host[i:]
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil || ip.IsUnspecified() || ip.IsMulticast() {
		return "", "", fmt.Errorf("invalid IPv6 address: %q", host)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", "", fmt.Errorf("invalid port: %q", port)
	}
	addr := ip.String() + zone
	return addr, net.JoinHostPort(addr, port), nil
}

func (s *Server) networks(w http.ResponseWriter, r *http.Request) {
	st := s.node.Status()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	current := s.currentNetwork(st)
	list := append([]Network(nil), s.store.networks...)
	found := false
	for i := range list {
		if strings.EqualFold(list[i].Name, current.Name) {
			list[i].Current = true
			found = true
		}
	}
	if !found && current.Name != "" {
		list = append(list, current)
	}
	writeJSON(w, map[string]any{"networks": list, "current": current})
}

// currentNetwork derives the active entry from the live node status.
func (s *Server) currentNetwork(st node.Status) Network {
	name := st.Network
	server := st.Server
	addr, _, err := normalizeController(server, "9000")
	if err != nil {
		addr = server
	}
	return Network{Name: name, IPv6: addr, Server: server, Current: true}
}

func (s *Server) joinNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "default"
	}
	addr, server, err := normalizeController(r.FormValue("ipv6"), "9000")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.node.Reconnect(server, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.store.mu.Lock()
	s.store.add(Network{Name: name, IPv6: addr, Server: server})
	s.store.mu.Unlock()
	writeJSON(w, map[string]string{"ok": "true", "name": name, "server": server})
}

func (s *Server) switchNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	s.store.mu.Lock()
	n, ok := s.store.find(name)
	s.store.mu.Unlock()
	if !ok {
		http.Error(w, "unknown network", http.StatusNotFound)
		return
	}
	if err := s.node.Reconnect(n.Server, n.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "name": n.Name, "server": n.Server})
}

func (s *Server) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	s.store.mu.Lock()
	ok := s.store.remove(name)
	s.store.mu.Unlock()
	if !ok {
		http.Error(w, "unknown network", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}
