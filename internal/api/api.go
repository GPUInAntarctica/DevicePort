// Package api exposes a small local HTTP control API and an embedded web GUI
// for a running DevicePort node. It uses only the standard library so the same
// UI works on Linux, Windows (Wine) and Android (browser on the device).
package api

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/GPUInAntarctica/DevicePort/internal/node"
)

//go:embed web/index.html
var webFS embed.FS

// Server serves the GUI and control API.
type Server struct {
	node  *node.Node
	store *networkStore
}

// New creates an API server bound to a node.
func New(n *node.Node) *Server {
	s := &Server{node: n, store: newNetworkStore()}
	// Make the node's current network immediately switchable, even on a fresh
	// install where the saved-network file does not exist yet.
	s.store.mu.Lock()
	if len(s.store.networks) == 0 {
		cur := s.currentNetwork(n.Status())
		cur.Current = false
		s.store.networks = []Network{cur}
		s.store.save()
	}
	s.store.mu.Unlock()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/status", s.status)
	mux.HandleFunc("/api/connect", s.connect)
	mux.HandleFunc("/api/send", s.send)
	mux.HandleFunc("/api/networks", s.networks)
	mux.HandleFunc("/api/networks/join", s.joinNetwork)
	mux.HandleFunc("/api/networks/switch", s.switchNetwork)
	mux.HandleFunc("/api/networks/delete", s.deleteNetwork)
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.node.Status())
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if len(id) != 32 {
		http.Error(w, "id must be a 32-character hex node ID", http.StatusBadRequest)
		return
	}
	if err := s.node.ConnectPeer(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "target": id})
}

// send injects a raw IP packet (hex) into the local virtual interface. It is
// mainly a demo/testing hook and is only available with a userspace TUN.
func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	pkt, err := hex.DecodeString(strings.TrimSpace(r.FormValue("hex")))
	if err != nil || len(pkt) < 20 {
		http.Error(w, "hex must be a raw IPv4 packet of at least 20 bytes", http.StatusBadRequest)
		return
	}
	if err := s.node.InjectPacket(pkt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "bytes": fmt.Sprintf("%d", len(pkt))})
}
