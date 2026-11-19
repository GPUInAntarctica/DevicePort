// Package server implements DevicePort's signaling and relay server.
//
// The server is intentionally simple and untrusted with payload contents:
//   - It assigns virtual IPv4 addresses from a per-network pool.
//   - It introduces peers to each other by exchanging public keys and IPv6
//     endpoints, enabling direct UDP hole punching.
//   - If a direct link cannot be established, peers send end-to-end encrypted
//     frames to the server, which forwards them blindly to the target.
package server

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"sync"
	"time"

	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/protocol"
)

const (
	staleAfter = 90 * time.Second
	reapEvery  = 30 * time.Second
)

// client is one connected node.
type client struct {
	conn       net.Conn
	writeMu    sync.Mutex
	id         string
	name       string
	network    string
	ip         string
	pubkey     []byte
	port       int
	endpoints  []string
	observed   string
	lastSeen   time.Time
	ready      bool
	authorized bool
}

func (c *client) send(m *protocol.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return protocol.WriteMessage(c.conn, m)
}

// networkState holds one virtual network (a /24 subnet).
type networkState struct {
	name        string
	idx         int // third octet of 10.66.idx.0/24
	prefix      string
	next        int                // next host octet
	peers       map[string]*client // authorized, active members
	pending     map[string]*client // connected but awaiting authorization
	authorized  map[string]bool    // persistent authorization decisions
	assigned    map[string]string  // pubkey ID -> stable virtual IP
	routes      []protocol.Route   // managed site-to-site routes
	requireAuth bool
}

// Server is the signaling/relay server.
type Server struct {
	mu                 sync.Mutex
	networks           map[string]*networkState
	clients            map[string]*client // id -> client (all networks)
	usedSubnets        map[int]bool
	defaultSubnet      string
	defaultRequireAuth bool
	authNetworks       map[string]bool
	noAuthNetworks     map[string]bool
	logger             *log.Logger
}

// SetDefaultSubnet forces the "default" network onto a specific /24 subnet.
// This is useful for Android VpnService, which must know the local address
// before the engine connects to the server.
func (s *Server) SetDefaultSubnet(prefix string) {
	s.mu.Lock()
	s.defaultSubnet = prefix
	s.mu.Unlock()
}

// SetDefaultRequireAuth sets the default authorization policy for new networks.
func (s *Server) SetDefaultRequireAuth(v bool) {
	s.mu.Lock()
	s.defaultRequireAuth = v
	s.mu.Unlock()
}

// SetNetworkAuth overrides the authorization policy for one network.
func (s *Server) SetNetworkAuth(name string, require bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if require {
		s.authNetworks[name] = true
		delete(s.noAuthNetworks, name)
	} else {
		s.noAuthNetworks[name] = true
		delete(s.authNetworks, name)
	}
	if n := s.networks[name]; n != nil {
		n.requireAuth = require
	}
}

// New creates an empty server.
func New(logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		networks:       make(map[string]*networkState),
		clients:        make(map[string]*client),
		usedSubnets:    make(map[int]bool),
		authNetworks:   make(map[string]bool),
		noAuthNetworks: make(map[string]bool),
		logger:         logger,
	}
}

func (s *Server) netFor(name string) *networkState {
	n := s.networks[name]
	if n == nil {
		var idx int
		forced := false
		if name == "default" && s.defaultSubnet != "" {
			var a, b, c, d int
			if _, err := fmt.Sscanf(s.defaultSubnet, "%d.%d.%d.%d", &a, &b, &c, &d); err == nil && a == 10 && b == 66 {
				idx, forced = c, true
			}
		}
		if !forced {
			h := fnv.New32a()
			h.Write([]byte(name))
			idx = int(h.Sum32()%250) + 1
			for s.usedSubnets[idx] {
				idx = idx%250 + 1
			}
		}
		s.usedSubnets[idx] = true
		require := s.defaultRequireAuth || s.authNetworks[name]
		if s.noAuthNetworks[name] {
			require = false
		}
		n = &networkState{
			name:        name,
			idx:         idx,
			prefix:      fmt.Sprintf("10.66.%d.0/24", idx),
			next:        2,
			peers:       make(map[string]*client),
			pending:     make(map[string]*client),
			authorized:  make(map[string]bool),
			assigned:    make(map[string]string),
			requireAuth: require,
		}
		s.networks[name] = n
	}
	return n
}

func (n *networkState) allocIP() string {
	if n.next > 254 {
		n.next = 2 // wrap (small demo pool)
	}
	ip := fmt.Sprintf("10.66.%d.%d", n.idx, n.next)
	n.next++
	return ip
}

// ipFor returns a stable virtual IP for a member ID.
func (n *networkState) ipFor(id string) string {
	if ip := n.assigned[id]; ip != "" {
		return ip
	}
	ip := n.allocIP()
	n.assigned[id] = ip
	return ip
}

// Serve accepts connections on addr until the listener is closed.
func (s *Server) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return s.ServeListener(ln)
}

// Listen creates a listener on addr and starts serving in the background. It
// returns the bound address, which is useful when port 0 was requested (tests).
func (s *Server) Listen(addr string) (net.Addr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	go func() { _ = s.ServeListener(ln) }()
	return ln.Addr(), nil
}

// ServeListener accepts connections on an existing listener.
func (s *Server) ServeListener(ln net.Listener) error {
	s.logger.Printf("DevicePort signaling/relay server listening on %s", ln.Addr())
	go s.reap()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-time.After(100 * time.Millisecond):
				continue
			default:
				return err
			}
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	c := &client{conn: conn, lastSeen: time.Now()}
	defer func() {
		s.remove(c)
		conn.Close()
	}()
	// Give each connection a deadline for the initial hello.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		m, err := protocol.ReadMessage(conn)
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(staleAfter))
		c.lastSeen = time.Now()
		if err := s.dispatch(c, m); err != nil {
			s.logger.Printf("client %s: %v", c.id, err)
			_ = c.send(&protocol.Message{Type: protocol.MsgError, Message: err.Error()})
			return
		}
	}
}

func (s *Server) dispatch(c *client, m *protocol.Message) error {
	switch m.Type {
	case protocol.MsgHello:
		return s.onHello(c, m)
	case protocol.MsgConnect:
		return s.onConnect(c, m)
	case protocol.MsgRelay:
		return s.onRelay(c, m)
	case protocol.MsgPing:
		return c.send(&protocol.Message{Type: protocol.MsgPong})
	default:
		return fmt.Errorf("unknown message type %q", m.Type)
	}
}

func (s *Server) onHello(c *client, m *protocol.Message) error {
	if len(m.PublicKey) != crypto.PublicKeySize {
		return errors.New("invalid public key")
	}
	id := crypto.NodeID(m.PublicKey)
	network := m.Network
	if network == "" {
		network = "default"
	}

	s.mu.Lock()
	if old := s.clients[id]; old != nil && old != c {
		old.conn.Close()
	}
	n := s.netFor(network)
	if c.ready && c.network == network {
		s.mu.Unlock()
		return errors.New("duplicate hello")
	}

	// Determine the address the server sees for this connection.
	var observed string
	if addr, ok := c.conn.RemoteAddr().(*net.TCPAddr); ok {
		ip := addr.IP
		if ip.To4() == nil {
			observed = ip.String()
		} else if v6 := ip.To16(); v6 != nil && v6[0] == 0 && v6[1] == 0 && v6[10] == 0xff && v6[11] == 0xff {
			// IPv4-mapped IPv6: keep as-is for IPv4 management clients.
			observed = ip.String()
		}
	}

	assigned := n.ipFor(id)
	c.id = id
	c.name = m.Name
	c.network = network
	c.ip = assigned
	c.pubkey = append([]byte(nil), m.PublicKey...)
	c.port = m.Port
	c.endpoints = append([]string(nil), m.Endpoints...)
	c.observed = observed
	c.ready = true
	c.authorized = !n.requireAuth || n.authorized[id]
	c.lastSeen = time.Now()
	if c.authorized {
		n.peers[id] = c
	} else {
		n.pending[id] = c
	}
	s.clients[id] = c

	welcome := &protocol.Message{
		Type:       protocol.MsgWelcome,
		YourIP:     assigned,
		Prefix:     n.prefix,
		RelayOK:    true,
		Authorized: c.authorized,
	}
	if c.authorized {
		for pid, peer := range n.peers {
			if pid == id {
				continue
			}
			welcome.Peers = append(welcome.Peers, s.peerInfo(peer))
		}
		welcome.Routes = append([]protocol.Route(nil), n.routes...)
	}
	s.mu.Unlock()

	if c.authorized {
		s.logger.Printf("node %s (%s) joined network %q as %s", short(id), c.name, network, assigned)
	} else {
		s.logger.Printf("node %s (%s) awaiting authorization on network %q", short(id), c.name, network)
	}
	if err := c.send(welcome); err != nil {
		return err
	}

	if c.authorized {
		s.mu.Lock()
		for pid, peer := range n.peers {
			if pid == id {
				continue
			}
			pi := s.peerInfo(c)
			_ = peer.send(&protocol.Message{Type: protocol.MsgPeer, Peer: &pi})
		}
		s.mu.Unlock()
	}
	return nil
}

// peerInfo builds the introduction record for a client.
func (s *Server) peerInfo(c *client) protocol.PeerInfo {
	eps := append([]string(nil), c.endpoints...)
	if c.observed != "" && c.port > 0 {
		candidate := net.JoinHostPort(c.observed, fmt.Sprintf("%d", c.port))
		found := false
		for _, e := range eps {
			if e == candidate {
				found = true
				break
			}
		}
		if !found {
			eps = append(eps, candidate)
		}
	}
	return protocol.PeerInfo{
		ID:        c.id,
		Name:      c.name,
		PublicKey: append([]byte(nil), c.pubkey...),
		IP:        c.ip,
		Endpoints: eps,
	}
}

func (s *Server) onConnect(c *client, m *protocol.Message) error {
	s.mu.Lock()
	target := s.clients[m.Target]
	var me, other *protocol.PeerInfo
	if target != nil && target.ready && target.authorized && c.authorized && target.network == c.network {
		mi := s.peerInfo(c)
		oi := s.peerInfo(target)
		me, other = &mi, &oi
	}
	s.mu.Unlock()
	if other == nil {
		return fmt.Errorf("peer %q not online", short(m.Target))
	}
	// Introduce both sides so they punch simultaneously (symmetric NATs
	// require both endpoints to send first).
	if err := c.send(&protocol.Message{Type: protocol.MsgPeer, Peer: other}); err != nil {
		return err
	}
	return target.send(&protocol.Message{Type: protocol.MsgPeer, Peer: me})
}

func (s *Server) onRelay(c *client, m *protocol.Message) error {
	if len(m.Data) == 0 || len(m.Data) > protocol.MaxPacket || m.To == "" {
		return errors.New("invalid relay message")
	}
	s.mu.Lock()
	target := s.clients[m.To]
	ok := target != nil && target.ready && target.authorized && c.authorized && target.network == c.network
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("relay target %q offline", short(m.To))
	}
	return target.send(&protocol.Message{
		Type: protocol.MsgRelay,
		From: c.id,
		Data: m.Data,
	})
}

func (s *Server) remove(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !c.ready {
		return
	}
	c.ready = false
	delete(s.clients, c.id)
	if n := s.networks[c.network]; n != nil {
		delete(n.peers, c.id)
		delete(n.pending, c.id)
		if c.authorized {
			for _, peer := range n.peers {
				_ = peer.send(&protocol.Message{Type: protocol.MsgPeerLeft, ID: c.id})
			}
		}
	}
	s.logger.Printf("node %s left", short(c.id))
}

func (s *Server) reap() {
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for range t.C {
		var stale []*client
		s.mu.Lock()
		for _, c := range s.clients {
			if time.Since(c.lastSeen) > staleAfter {
				stale = append(stale, c)
			}
		}
		s.mu.Unlock()
		for _, c := range stale {
			_ = c.conn.Close()
		}
	}
}

// Authorize grants or revokes a member's access to a network. When granting
// access to a connected member, the node is welcomed immediately and all other
// members are told about it.
func (s *Server) Authorize(network, id string, allow bool) error {
	s.mu.Lock()
	n := s.networks[network]
	if n == nil {
		s.mu.Unlock()
		return fmt.Errorf("unknown network %q", network)
	}
	n.authorized[id] = allow
	c := s.clients[id]
	var welcome *protocol.Message
	var peers []*client
	if c != nil && c.network == network {
		c.authorized = allow
		if allow {
			delete(n.pending, id)
			n.peers[id] = c
			welcome = &protocol.Message{
				Type:       protocol.MsgWelcome,
				YourIP:     c.ip,
				Prefix:     n.prefix,
				RelayOK:    true,
				Authorized: true,
				Routes:     append([]protocol.Route(nil), n.routes...),
			}
			for pid, peer := range n.peers {
				if pid == id {
					continue
				}
				welcome.Peers = append(welcome.Peers, s.peerInfo(peer))
				peers = append(peers, peer)
			}
		} else {
			delete(n.peers, id)
			n.pending[id] = c
			for _, peer := range n.peers {
				peers = append(peers, peer)
			}
		}
	} else if !allow {
		delete(n.peers, id)
	}
	s.mu.Unlock()

	if welcome != nil {
		_ = c.send(welcome)
	}
	if !allow {
		for _, peer := range peers {
			_ = peer.send(&protocol.Message{Type: protocol.MsgPeerLeft, ID: id})
		}
		return nil
	}
	if c != nil {
		pi := s.peerInfoSafe(network, id)
		for _, peer := range peers {
			_ = peer.send(&protocol.Message{Type: protocol.MsgPeer, Peer: pi})
		}
	}
	return nil
}

// peerInfoSafe reads a peer record under the server lock.
func (s *Server) peerInfoSafe(network, id string) *protocol.PeerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.clients[id]
	if c == nil || c.network != network {
		return nil
	}
	pi := s.peerInfo(c)
	return &pi
}

// AddRoute adds or replaces a managed route for a network and pushes the full
// route list to every authorized member.
func (s *Server) AddRoute(network, cidr, via string) error {
	s.mu.Lock()
	n := s.networks[network]
	if n == nil {
		s.mu.Unlock()
		return fmt.Errorf("unknown network %q", network)
	}
	for _, r := range n.routes {
		if r.CIDR == cidr {
			s.mu.Unlock()
			return nil
		}
	}
	n.routes = append(n.routes, protocol.Route{CIDR: cidr, Via: via})
	s.broadcastRoutesLocked(n)
	s.mu.Unlock()
	return nil
}

// RemoveRoute deletes a managed route.
func (s *Server) RemoveRoute(network, cidr string) error {
	s.mu.Lock()
	n := s.networks[network]
	if n == nil {
		s.mu.Unlock()
		return fmt.Errorf("unknown network %q", network)
	}
	out := n.routes[:0]
	for _, r := range n.routes {
		if r.CIDR != cidr {
			out = append(out, r)
		}
	}
	n.routes = out
	s.broadcastRoutesLocked(n)
	s.mu.Unlock()
	return nil
}

func (s *Server) broadcastRoutesLocked(n *networkState) {
	m := &protocol.Message{Type: protocol.MsgRoutes, Routes: append([]protocol.Route(nil), n.routes...)}
	for _, peer := range n.peers {
		_ = peer.send(m)
	}
}

// MemberView is a member snapshot for the admin API.
type MemberView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IP         string `json:"ip"`
	Authorized bool   `json:"authorized"`
	Online     bool   `json:"online"`
}

// NetworkView is a network snapshot for the admin API.
type NetworkView struct {
	Name        string           `json:"name"`
	Prefix      string           `json:"prefix"`
	RequireAuth bool             `json:"require_auth"`
	Routes      []protocol.Route `json:"routes"`
	Members     []MemberView     `json:"members"`
}

// Networks returns a snapshot of all networks for the admin API.
func (s *Server) Networks() []NetworkView {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []NetworkView
	for name, n := range s.networks {
		v := NetworkView{Name: name, Prefix: n.prefix, RequireAuth: n.requireAuth, Routes: append([]protocol.Route(nil), n.routes...)}
		for _, c := range n.peers {
			v.Members = append(v.Members, MemberView{ID: c.id, Name: c.name, IP: c.ip, Authorized: true, Online: time.Since(c.lastSeen) < staleAfter})
		}
		for _, c := range n.pending {
			v.Members = append(v.Members, MemberView{ID: c.id, Name: c.name, IP: c.ip, Online: time.Since(c.lastSeen) < staleAfter})
		}
		out = append(out, v)
	}
	return out
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
