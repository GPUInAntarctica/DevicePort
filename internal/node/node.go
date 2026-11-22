// Package node implements the DevicePort overlay engine: it owns the virtual
// TUN device, the encrypted UDP data plane, NAT hole punching, relay fallback
// and the signaling control connection.
package node

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/protocol"
	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

const (
	punchInterval     = 200 * time.Millisecond
	punchBurst        = 15 * time.Second
	punchRetry        = 12 * time.Second
	keepaliveInterval = 5 * time.Second
	directTimeout     = 60 * time.Second
	controlPing       = 20 * time.Second
	bufferSize        = 65535
	onlineTimeout     = 15 * time.Second
	defaultMaxDirect  = 4
	replayWindowBits  = 64
)

// Options configures a Node.
type Options struct {
	Server  string
	Network string
	Name    string
	Logger  *log.Logger
	// AutoConnect causes discovered peers to be linked automatically.
	AutoConnect bool
	// MaxDirect caps how many peers this node will punch directly. Remaining
	// peers are reachable through the server relay. 0 means unlimited (full
	// mesh); the default is a small number to avoid O(n²) punch traffic.
	MaxDirect int
	// LANDiscovery enables IPv6 multicast peer discovery on the local network.
	// Peers found this way are introduced directly, without waiting for the
	// signaling server, and still use the same encrypted data plane.
	LANDiscovery bool
	// DisplayIPv6 is an optional list supplied by the host app (e.g. Android,
	// where /proc/net/if_inet6 is blocked by SELinux). Used only for display.
	DisplayIPv6 []string
}

// Peer holds the runtime state of one remote node.
type Peer struct {
	Info      protocol.PeerInfo
	Key       []byte
	Endpoints []string

	direct     bool
	relayOnly  bool
	activeEP   string
	punchUntil time.Time
	lastTry    time.Time
	lastSeen   time.Time
	tx, rx     uint64
	dropped    uint64

	// monotonic send sequence and a 64-bit sliding receive window
	txSeq        uint64
	replayHigh   uint64
	replayBitmap uint64
	rtt          time.Duration
}

// route is a managed route pushed by the server: traffic for CIDR is sent to
// the peer with ID Via, which acts as the gateway to that subnet.
type route struct {
	cidr  *net.IPNet
	viaID string
}

// RouteStatus is a managed route as reported to the GUI.
type RouteStatus struct {
	CIDR string `json:"cidr"`
	Via  string `json:"via"`
}

// PeerStatus is a snapshot for the GUI/CLI.
type PeerStatus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IP        string    `json:"ip"`
	Direct    bool      `json:"direct"`
	RelayOnly bool      `json:"relay_only"`
	Online    bool      `json:"online"`
	Endpoint  string    `json:"endpoint"`
	Mode      string    `json:"mode"`
	TX        uint64    `json:"tx"`
	RX        uint64    `json:"rx"`
	Dropped   uint64    `json:"dropped"`
	RTTMs     float64   `json:"rtt_ms"`
	LastSeen  time.Time `json:"last_seen"`
}

// Status is a snapshot of the whole node for the GUI/CLI.
type Status struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	IP           string        `json:"ip"`
	Prefix       string        `json:"prefix"`
	Network      string        `json:"network"`
	Server       string        `json:"server"`
	UDPAddr      string        `json:"udp_addr"`
	IPv6         []string      `json:"ipv6"`
	LANDiscovery bool          `json:"lan_discovery"`
	Encryption   string        `json:"encryption"`
	Authorized   bool          `json:"authorized"`
	Routes       []RouteStatus `json:"routes"`
	Uptime       string        `json:"uptime"`
	Peers        []PeerStatus  `json:"peers"`
}

// Node is a DevicePort client instance.
type Node struct {
	ident  *crypto.Identity
	dev    tun.Device
	opts   Options
	logger *log.Logger

	udp       *net.UDPConn
	udpMu     sync.RWMutex // guards udp so it can be replaced on network switch
	control   net.Conn
	controlMu sync.Mutex

	mu         sync.Mutex
	peers      map[string]*Peer
	byIP       map[string]*Peer
	byShortID  map[string]*Peer
	routes     []route
	authorized bool
	selfIP     string
	prefix     string
	started    time.Time

	// pending keepalive tokens used for RTT measurement without clock sync
	pings map[uint64]time.Time

	closed        chan struct{}
	closeOne      sync.Once
	wg            sync.WaitGroup // control, udp and maintenance loops
	tunWG         sync.WaitGroup // tun loop outlives a network switch
	tunStarted    bool
	running       atomic.Bool
	stopDiscovery func()

	// OnChange is called (in a goroutine) whenever peer state changes.
	OnChange func()
	// OnAddress is called when the server assigns a virtual IP. Native TUN
	// implementations use it to configure the interface address.
	OnAddress func(ip, prefix string) error
}

// New creates a node. It opens the UDP socket immediately but does not
// connect to the server until Start is called.
func New(ident *crypto.Identity, dev tun.Device, opts Options) (*Node, error) {
	if ident == nil || dev == nil {
		return nil, errors.New("identity and tun device are required")
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.Network == "" {
		opts.Network = "default"
	}
	if opts.Name == "" {
		opts.Name = "device"
	}
	if opts.MaxDirect == 0 {
		opts.MaxDirect = defaultMaxDirect
	}
	if opts.MaxDirect < 0 {
		opts.MaxDirect = 0 // 0 means unlimited (full mesh)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6zero, Port: 0})
	if err != nil {
		// Fall back to IPv4 wildcard on hosts without IPv6.
		udp, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			return nil, fmt.Errorf("listen udp: %w", err)
		}
	}
	return &Node{
		ident:     ident,
		dev:       dev,
		opts:      opts,
		logger:    opts.Logger,
		udp:       udp,
		peers:     make(map[string]*Peer),
		byIP:      make(map[string]*Peer),
		byShortID: make(map[string]*Peer),
		pings:     make(map[uint64]time.Time),
		closed:    make(chan struct{}),
		started:   time.Now(),
	}, nil
}

// UDPAddr returns the bound UDP address (useful for tests).
func (n *Node) UDPAddr() net.Addr {
	n.udpMu.RLock()
	defer n.udpMu.RUnlock()
	return n.udp.LocalAddr()
}

func (n *Node) udpPort() int {
	n.udpMu.RLock()
	defer n.udpMu.RUnlock()
	a, ok := n.udp.LocalAddr().(*net.UDPAddr)
	if !ok {
		return 0
	}
	return a.Port
}

func (n *Node) writeToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	n.udpMu.RLock()
	defer n.udpMu.RUnlock()
	if n.udp == nil {
		return 0, errors.New("udp socket closed")
	}
	return n.udp.WriteToUDP(b, addr)
}

func (n *Node) udpString() string {
	n.udpMu.RLock()
	defer n.udpMu.RUnlock()
	if n.udp == nil {
		return "—"
	}
	return n.udp.LocalAddr().String()
}

// Start connects to the signaling server and launches all worker loops.
func (n *Node) Start() error {
	conn, err := net.DialTimeout("tcp", n.opts.Server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect signaling server %s: %w", n.opts.Server, err)
	}
	n.control = conn
	port := n.udpPort()
	hello := &protocol.Message{
		Type:      protocol.MsgHello,
		ID:        n.ident.ID(),
		Name:      n.opts.Name,
		PublicKey: n.ident.PublicKey(),
		Network:   n.opts.Network,
		Port:      port,
		Endpoints: localEndpoints(port),
	}
	if err := protocol.WriteMessage(conn, hello); err != nil {
		conn.Close()
		return fmt.Errorf("send hello: %w", err)
	}

	n.running.Store(true)
	n.wg.Add(3)
	go n.controlLoop()
	go n.udpLoop()
	go n.maintenanceLoop()
	// The TUN device is kept open across a network switch, so its read loop is
	// started only once. It reads n.udp and the peer maps dynamically.
	if !n.tunStarted {
		n.tunStarted = true
		n.tunWG.Add(1)
		go n.tunLoop()
	}
	if n.opts.LANDiscovery {
		n.stopDiscovery = n.startDiscovery()
		n.logger.Printf("LAN discovery enabled (IPv6 multicast port %d)", LANDiscoveryPort)
	}
	n.logger.Printf("node %s started (udp %s, server %s, network %s)",
		short(n.ident.ID()), n.udpString(), n.opts.Server, n.opts.Network)
	return nil
}

// Reconnect stops the current signaling/UDP workers and connects to a different
// controller or network while keeping the same identity and TUN device. This
// lets the GUI join or create another network without restarting the app.
func (n *Node) Reconnect(server, network string) error {
	server = strings.TrimSpace(server)
	if server == "" {
		return errors.New("controller address is required")
	}
	network = strings.TrimSpace(network)
	if network == "" {
		network = "default"
	}
	if n.running.Load() {
		n.stopWorkers()
	}

	// Reset per-network state while the TUN loop is the only remaining reader.
	n.mu.Lock()
	n.peers = make(map[string]*Peer)
	n.byIP = make(map[string]*Peer)
	n.byShortID = make(map[string]*Peer)
	n.pings = make(map[uint64]time.Time)
	n.routes = nil
	n.authorized = false
	n.selfIP = ""
	n.prefix = ""
	n.mu.Unlock()

	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6zero, Port: 0})
	if err != nil {
		udp, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			return fmt.Errorf("listen udp: %w", err)
		}
	}
	n.udpMu.Lock()
	old := n.udp
	n.udp = udp
	n.udpMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	n.closed = make(chan struct{})
	n.closeOne = sync.Once{}
	n.stopDiscovery = nil
	n.opts.Server = server
	n.opts.Network = network
	n.started = time.Now()
	return n.Start()
}

// stopWorkers stops the control, UDP, maintenance and discovery loops. It does
// not close the TUN device, so Reconnect can reuse it.
func (n *Node) stopWorkers() {
	n.running.Store(false)
	select {
	case <-n.closed:
	default:
		close(n.closed)
	}
	if n.stopDiscovery != nil {
		n.stopDiscovery()
		n.stopDiscovery = nil
	}
	n.controlMu.Lock()
	if n.control != nil {
		_ = n.control.Close()
	}
	n.controlMu.Unlock()
	n.udpMu.RLock()
	if n.udp != nil {
		_ = n.udp.Close()
	}
	n.udpMu.RUnlock()
	n.wg.Wait()
}

// Close stops all loops and releases resources.
func (n *Node) Close() error {
	var err error
	n.closeOne.Do(func() {
		n.stopWorkers()
		if n.dev != nil {
			err = n.dev.Close()
		}
		n.tunWG.Wait()
	})
	return err
}

// InjectPacket injects a raw IPv4 packet into the virtual interface when the
// device supports it (the userspace and Android test paths). It is primarily
// used by the GUI's "send test packet" action and by end-to-end tests.
func (n *Node) InjectPacket(pkt []byte) error {
	inj, ok := n.dev.(interface{ Inject([]byte) error })
	if !ok {
		return errors.New("active TUN device does not support packet injection")
	}
	return inj.Inject(pkt)
}

// ConnectPeer asks the server to introduce us to target.
func (n *Node) ConnectPeer(target string) error {
	if n.control == nil {
		return errors.New("node not started")
	}
	return n.sendControl(&protocol.Message{Type: protocol.MsgConnect, Target: target})
}

func (n *Node) sendControl(m *protocol.Message) error {
	n.controlMu.Lock()
	defer n.controlMu.Unlock()
	if n.control == nil {
		return errors.New("control connection closed")
	}
	return protocol.WriteMessage(n.control, m)
}

func (n *Node) notify() {
	if n.OnChange != nil {
		go n.OnChange()
	}
}

// ---------------------------------------------------------------- control plane

func (n *Node) controlLoop() {
	defer n.wg.Done()
	for {
		m, err := protocol.ReadMessage(n.control)
		if err != nil {
			if n.running.Load() {
				n.logger.Printf("control connection lost: %v", err)
			}
			return
		}
		switch m.Type {
		case protocol.MsgWelcome:
			n.mu.Lock()
			n.selfIP = m.YourIP
			n.prefix = m.Prefix
			n.authorized = m.Authorized
			n.mu.Unlock()
			n.logger.Printf("assigned virtual IP %s (%s) authorized=%v", m.YourIP, m.Prefix, m.Authorized)
			if n.OnAddress != nil {
				if err := n.OnAddress(m.YourIP, m.Prefix); err != nil {
					n.logger.Printf("configure tun address: %v", err)
				}
			}
			for i := range m.Peers {
				n.learnPeer(&m.Peers[i])
			}
			n.setRoutes(m.Routes)
			n.recomputeLinks()
			n.notify()
		case protocol.MsgRoutes:
			n.setRoutes(m.Routes)
			n.notify()
		case protocol.MsgPeer:
			if m.Peer != nil {
				n.learnPeer(m.Peer)
				n.recomputeLinks()
				n.notify()
			}
		case protocol.MsgPeerLeft:
			n.forgetPeer(m.ID)
			n.recomputeLinks()
			n.notify()
		case protocol.MsgRelay:
			n.handleFrame(m.Data, nil)
		case protocol.MsgPong:
			// lastSeen is refreshed on any control traffic in the server.
		case protocol.MsgError:
			n.logger.Printf("server error: %s", m.Message)
		default:
			n.logger.Printf("unknown control message %q", m.Type)
		}
	}
}

// learnPeer registers a peer and derives its symmetric key from the ECDH of
// our private key and the peer's public key received from the server.
func (n *Node) learnPeer(info *protocol.PeerInfo) {
	if info == nil || info.ID == n.ident.ID() {
		return
	}
	// LAN discovery announces a peer without a server-assigned virtual IP.
	// Never let that empty IP erase one we already learned from the server.
	infoCopy := *info
	key, err := crypto.SharedKey(n.ident.PrivateKey(), info.PublicKey)
	if err != nil {
		n.logger.Printf("peer %s: key agreement failed: %v", short(info.ID), err)
		return
	}
	shortID, err := crypto.ShortID(info.ID)
	if err != nil {
		return
	}
	sid := hex.EncodeToString(shortID)

	n.mu.Lock()
	p := n.peers[info.ID]
	isNew := p == nil
	if p == nil {
		now := time.Now()
		p = &Peer{lastTry: now, punchUntil: now.Add(punchBurst)}
		n.peers[info.ID] = p
		n.byShortID[sid] = p
	}
	oldEndpoints := append([]string(nil), p.Endpoints...)
	oldIP := p.Info.IP
	if infoCopy.IP == "" && oldIP != "" {
		infoCopy.IP = oldIP
	}
	p.Info = infoCopy
	p.Key = key
	p.Endpoints = append([]string(nil), infoCopy.Endpoints...)
	endpointsChanged := len(oldEndpoints) != len(infoCopy.Endpoints)
	if !endpointsChanged {
		for i := range infoCopy.Endpoints {
			if oldEndpoints[i] != infoCopy.Endpoints[i] {
				endpointsChanged = true
				break
			}
		}
	}
	if oldIP != "" && oldIP != infoCopy.IP {
		delete(n.byIP, oldIP)
	}
	if infoCopy.IP != "" {
		n.byIP[infoCopy.IP] = p
	}
	if !p.direct && time.Since(p.lastTry) > punchRetry {
		p.punchUntil = time.Now().Add(punchBurst)
		p.lastTry = time.Now()
	}
	n.mu.Unlock()
	if isNew || endpointsChanged || (oldIP != infoCopy.IP) {
		n.logger.Printf("peer %s (%s) at %s, %d endpoint(s): %v",
			short(info.ID), infoCopy.Name, infoCopy.IP, len(infoCopy.Endpoints), infoCopy.Endpoints)
	}
}

// setRoutes parses managed routes pushed by the server. Invalid CIDRs are
// ignored so a bad admin entry cannot break the data plane.
func (n *Node) setRoutes(rs []protocol.Route) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.routes = nil
	for _, r := range rs {
		_, cidr, err := net.ParseCIDR(r.CIDR)
		if err != nil {
			n.logger.Printf("invalid managed route %q: %v", r.CIDR, err)
			continue
		}
		n.routes = append(n.routes, route{cidr: cidr, viaID: r.Via})
	}
}

// recomputeLinks limits the number of direct links to MaxDirect. Peers are
// ranked by XOR distance from our own ID, which keeps the choice stable and
// roughly symmetric; the rest are reached through the relay. This prevents the
// O(n²) punch traffic of a full mesh.
func (n *Node) recomputeLinks() {
	n.mu.Lock()
	defer n.mu.Unlock()
	self, _ := hex.DecodeString(n.ident.ID())
	type candidate struct {
		id   string
		dist uint64
	}
	var ids []candidate
	for id := range n.peers {
		b, err := hex.DecodeString(id)
		if err != nil || len(b) < 8 || len(self) < 8 {
			continue
		}
		ids = append(ids, candidate{id, binary.BigEndian.Uint64(b[:8]) ^ binary.BigEndian.Uint64(self[:8])})
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].dist < ids[j].dist })
	selected := make(map[string]bool)
	for i, c := range ids {
		if n.opts.MaxDirect > 0 && i >= n.opts.MaxDirect {
			break
		}
		selected[c.id] = true
	}
	for id, p := range n.peers {
		p.relayOnly = !selected[id]
		if p.relayOnly && p.direct {
			p.direct = false
			p.activeEP = ""
		}
	}
}

func (n *Node) forgetPeer(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.peers[id]
	if p == nil {
		return
	}
	sid, _ := crypto.ShortID(id)
	delete(n.byShortID, hex.EncodeToString(sid))
	delete(n.byIP, p.Info.IP)
	delete(n.peers, id)
	n.logger.Printf("peer %s left", short(id))
}

// ---------------------------------------------------------------- data plane

func (n *Node) udpLoop() {
	defer n.wg.Done()
	buf := make([]byte, bufferSize)
	for {
		n.udpMu.RLock()
		udp := n.udp
		n.udpMu.RUnlock()
		if udp == nil {
			return
		}
		nb, src, err := udp.ReadFromUDP(buf)
		if err != nil {
			if n.running.Load() {
				n.logger.Printf("udp read: %v", err)
			}
			return
		}
		frame := append([]byte(nil), buf[:nb]...)
		n.handleFrame(frame, src)
	}
}

// handleFrame authenticates, decrypts and dispatches one data-plane frame.
// src is the observed UDP source address; it is nil for relayed frames.
func (n *Node) handleFrame(frame []byte, src *net.UDPAddr) {
	shortID, nonce, ciphertext, seq, frameRelayed, err := protocol.ParseFrame(frame)
	if err != nil {
		return
	}
	aad, err := protocol.AAD(frame)
	if err != nil {
		return
	}
	sid := hex.EncodeToString(shortID)

	n.mu.Lock()
	p := n.byShortID[sid]
	n.mu.Unlock()
	if p == nil {
		return // unknown sender
	}
	plain, err := crypto.Open(p.Key, nonce, ciphertext, aad)
	if err != nil {
		return // wrong key or tampered packet: drop silently
	}
	if len(plain) < 1 {
		return
	}

	n.mu.Lock()
	if !acceptSeq(p, seq) {
		p.dropped++
		n.mu.Unlock()
		return // replayed or out-of-window packet
	}
	p.lastSeen = time.Now()
	if src != nil && !frameRelayed && !p.relayOnly {
		// A valid direct packet confirms the path.
		p.activeEP = src.String()
		p.direct = true
	}
	n.mu.Unlock()
	atomic.AddUint64(&p.rx, 1)

	switch plain[0] {
	case protocol.TypeData:
		if len(plain) > 1 {
			if _, err := n.dev.Write(plain[1:]); err != nil {
				n.logger.Printf("tun write: %v", err)
			}
		}
	case protocol.TypePunch:
		if src != nil && !frameRelayed && !p.relayOnly {
			n.mu.Lock()
			p.activeEP = src.String()
			p.direct = true
			p.punchUntil = time.Time{}
			n.mu.Unlock()
			n.notify()
		}
	case protocol.TypeKeepalive:
		if len(plain) < 10 {
			return
		}
		token := binary.BigEndian.Uint64(plain[1:9])
		switch plain[9] {
		case protocol.KeepaliveRequest:
			// Echo the token back; the initiator measures RTT locally.
			_ = n.sendKeepalive(p, token, protocol.KeepaliveResponse)
		case protocol.KeepaliveResponse:
			n.mu.Lock()
			sent, ok := n.pings[token]
			if ok {
				p.rtt = time.Since(sent)
				delete(n.pings, token)
			}
			n.mu.Unlock()
		}
	}
}

// acceptSeq implements a 64-bit sliding replay window. It must be called with
// n.mu held. Sequence numbers start at 1; 0 is treated as "nothing received".
func acceptSeq(p *Peer, seq uint64) bool {
	if seq == 0 {
		return false
	}
	if seq > p.replayHigh {
		if delta := seq - p.replayHigh; delta >= replayWindowBits {
			p.replayBitmap = 0
		} else {
			p.replayBitmap <<= delta
		}
		p.replayHigh = seq
		p.replayBitmap |= 1 // bit 0 = highest sequence, marked as seen
		return true
	}
	delta := p.replayHigh - seq
	if delta >= replayWindowBits {
		return false
	}
	bit := uint64(1) << delta
	if p.replayBitmap&bit != 0 {
		return false
	}
	p.replayBitmap |= bit
	return true
}

// tunLoop reads raw IPv4 packets from the virtual interface and forwards them
// to the peer that owns the destination address.
func (n *Node) tunLoop() {
	defer n.tunWG.Done()
	buf := make([]byte, bufferSize)
	for {
		nb, err := n.dev.Read(buf)
		if err != nil {
			if n.running.Load() {
				n.logger.Printf("tun read: %v", err)
			}
			return
		}
		if nb < 20 {
			continue
		}
		pkt := append([]byte(nil), buf[:nb]...)
		// IPv4 header: version/IHL at byte 0, destination at bytes 16..19.
		if pkt[0]>>4 != 4 {
			continue
		}
		dstIP := net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19])
		dst := dstIP.String()
		n.mu.Lock()
		p := n.byIP[dst]
		if p == nil {
			// Managed site-to-site route: forward to the gateway member.
			for _, r := range n.routes {
				if r.cidr.Contains(dstIP) {
					p = n.peers[r.viaID]
					break
				}
			}
		}
		n.mu.Unlock()
		if p == nil {
			continue // not a peer or managed route, leave to the local stack
		}
		if err := n.sendToPeer(p, protocol.TypeData, pkt); err != nil {
			n.logger.Printf("send to %s: %v", short(p.Info.ID), err)
		}
	}
}

// sendToPeer encrypts a payload and sends it either directly over IPv6 UDP or,
// if no direct path is established, through the server's relay.
func (n *Node) sendToPeer(p *Peer, typ byte, payload []byte) error {
	n.mu.Lock()
	direct := p.direct && p.activeEP != "" && !p.relayOnly
	ep := p.activeEP
	n.mu.Unlock()
	if direct {
		return n.sendFrame(p, typ, payload, ep, false)
	}
	return n.sendFrame(p, typ, payload, "", true)
}

// sendFrame builds an authenticated frame and delivers it either to a specific
// UDP endpoint (direct/hole punching) or through the server (relay). Punch
// packets always use this with relayed=false so that they open a NAT binding.
func (n *Node) sendFrame(p *Peer, typ byte, payload []byte, ep string, relayed bool) error {
	plain := make([]byte, 1+len(payload))
	plain[0] = typ
	copy(plain[1:], payload)

	// The frame header identifies the SENDER (us), so the receiver can look up
	// the correct peer key. Using the target's ID here would make every frame
	// appear to come from the local node.
	sid, err := crypto.ShortID(n.ident.ID())
	if err != nil {
		return err
	}
	nonce, err := crypto.RandomNonce()
	if err != nil {
		return err
	}
	seq := atomic.AddUint64(&p.txSeq, 1)

	// Build the header first; it is the GCM additional authenticated data.
	flags := byte(0)
	if relayed {
		flags = protocol.FlagRelayed
	}
	header := make([]byte, protocol.HeaderSize)
	header[0], header[1], header[2], header[3] = protocol.Magic0, protocol.Magic1, protocol.Version, flags
	copy(header[4:12], sid)
	binary.BigEndian.PutUint64(header[protocol.SeqOffset:protocol.NonceOffset], seq)

	ct, err := crypto.Seal(p.Key, nonce, plain, header)
	if err != nil {
		return err
	}
	frame, err := protocol.BuildFrame(sid, seq, nonce, ct, relayed)
	if err != nil {
		return err
	}

	if !relayed {
		addr, err := net.ResolveUDPAddr("udp", ep)
		if err != nil {
			return err
		}
		_, err = n.writeToUDP(frame, addr)
		if err != nil {
			n.logger.Printf("direct send to %s failed: %v", ep, err)
		}
		atomic.AddUint64(&p.tx, 1)
		return err
	}
	return n.sendControl(&protocol.Message{
		Type: protocol.MsgRelay,
		To:   p.Info.ID,
		Data: frame,
	})
}

// sendKeepalive sends a keepalive carrying an 8-byte echo token. Requests are
// answered by the peer with the same token so RTT can be measured locally.
func (n *Node) sendKeepalive(p *Peer, token uint64, dir byte) error {
	payload := make([]byte, 9)
	binary.BigEndian.PutUint64(payload[:8], token)
	payload[8] = dir
	n.mu.Lock()
	direct := p.direct && p.activeEP != "" && !p.relayOnly
	ep := p.activeEP
	n.mu.Unlock()
	if dir == protocol.KeepaliveRequest {
		n.mu.Lock()
		n.pings[token] = time.Now()
		// Bound the pending map in case responses are lost.
		if len(n.pings) > 256 {
			for k, t := range n.pings {
				if time.Since(t) > 30*time.Second {
					delete(n.pings, k)
				}
			}
		}
		n.mu.Unlock()
	}
	if direct {
		return n.sendFrame(p, protocol.TypeKeepalive, payload, ep, false)
	}
	return n.sendFrame(p, protocol.TypeKeepalive, payload, "", true)
}

// ---------------------------------------------------------------- maintenance

func (n *Node) maintenanceLoop() {
	defer n.wg.Done()
	punchTicker := time.NewTicker(punchInterval)
	kaTicker := time.NewTicker(keepaliveInterval)
	pingTicker := time.NewTicker(controlPing)
	defer punchTicker.Stop()
	defer kaTicker.Stop()
	defer pingTicker.Stop()

	for {
		select {
		case <-n.closed:
			return
		case <-pingTicker.C:
			_ = n.sendControl(&protocol.Message{Type: protocol.MsgPing})
		case <-kaTicker.C:
			n.sendKeepalives()
		case <-punchTicker.C:
			n.doPunch()
		}
	}
}

func (n *Node) sendKeepalives() {
	n.mu.Lock()
	var peers []*Peer
	for _, p := range n.peers {
		if time.Since(p.lastSeen) > directTimeout && p.direct {
			p.direct = false
			p.activeEP = ""
			p.punchUntil = time.Now().Add(punchBurst)
			p.lastTry = time.Now()
		}
		peers = append(peers, p)
	}
	n.mu.Unlock()
	for _, p := range peers {
		var token uint64
		var b [8]byte
		if _, err := io.ReadFull(rand.Reader, b[:]); err == nil {
			token = binary.BigEndian.Uint64(b[:])
		} else {
			token = uint64(time.Now().UnixNano())
		}
		_ = n.sendKeepalive(p, token, protocol.KeepaliveRequest)
	}
}

func (n *Node) doPunch() {
	now := time.Now()
	n.mu.Lock()
	type punchTarget struct {
		peer *Peer
		eps  []string
	}
	var targets []punchTarget
	for _, p := range n.peers {
		if p.direct || p.relayOnly {
			continue
		}
		if now.After(p.punchUntil) {
			if now.Sub(p.lastTry) < punchRetry {
				continue
			}
			p.punchUntil = now.Add(punchBurst)
			p.lastTry = now
		}
		targets = append(targets, punchTarget{p, append([]string(nil), p.Endpoints...)})
	}
	n.mu.Unlock()
	for _, t := range targets {
		for _, ep := range t.eps {
			// Always send punches as raw UDP datagrams to open a NAT binding;
			// relaying a punch would never establish a direct path.
			if err := n.sendFrame(t.peer, protocol.TypePunch, nil, ep, false); err != nil && n.logger != nil {
				n.logger.Printf("punch %s: %v", ep, err)
			}
		}
	}
}

// Status returns a thread-safe snapshot for the API.
func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := Status{
		ID:           n.ident.ID(),
		Name:         n.opts.Name,
		IP:           n.selfIP,
		Prefix:       n.prefix,
		Network:      n.opts.Network,
		Server:       n.opts.Server,
		UDPAddr:      n.udpString(),
		IPv6:         n.displayIPv6(),
		LANDiscovery: n.opts.LANDiscovery,
		Encryption:   "X25519 + HKDF-SHA256 + AES-256-GCM",
		Authorized:   n.authorized,
	}
	if n.running.Load() {
		s.Uptime = time.Since(n.started).Truncate(time.Second).String()
	}
	for _, r := range n.routes {
		s.Routes = append(s.Routes, RouteStatus{CIDR: r.cidr.String(), Via: r.viaID})
	}
	for _, p := range n.peers {
		mode := "relay"
		if p.direct {
			mode = "direct"
		}
		s.Peers = append(s.Peers, PeerStatus{
			ID: p.Info.ID, Name: p.Info.Name, IP: p.Info.IP,
			Direct: p.direct, RelayOnly: p.relayOnly,
			Online:   time.Since(p.lastSeen) < onlineTimeout,
			Endpoint: p.activeEP, Mode: mode,
			TX: atomic.LoadUint64(&p.tx), RX: atomic.LoadUint64(&p.rx),
			Dropped: p.dropped, RTTMs: float64(p.rtt.Microseconds()) / 1000.0,
			LastSeen: p.lastSeen,
		})
	}
	return s
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// localEndpoints returns the local IPv6 addresses on which peers may reach us.
func localEndpoints(port int) []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, a := range addrs {
				ipnet, ok := a.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipnet.IP
				if ip.To4() != nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
					continue
				}
				out = append(out, net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)))
			}
		}
	}
	// Loopback is essential for local testing and harmless otherwise.
	out = append(out, net.JoinHostPort("::1", fmt.Sprintf("%d", port)))
	return out
}

func (n *Node) displayIPv6() []string {
	if len(n.opts.DisplayIPv6) > 0 {
		return n.opts.DisplayIPv6
	}
	return localIPv6Addresses()
}

// localIPv6Addresses returns the node's usable IPv6 addresses for display in
// the console. Global addresses are preferred; link-local addresses are kept
// because they are what LAN discovery uses. IPv4 and loopback are omitted.
func localIPv6Addresses() []string {
	var out []string
	seen := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		ifaces = nil // /proc fallback below still works on Linux/Android
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip.To4() != nil || ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			// Include global, link-local and legacy site-local (fec0::) addresses:
			// emulators and some mobile networks only provide the latter.
			s := ip.String()
			if ip.IsLinkLocalUnicast() && iface.Name != "" {
				s += "%" + iface.Name
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	// On Android the Go runtime can miss IPv6 addresses exposed by netlink.
	// /proc/net/if_inet6 is authoritative on Linux-based systems.
	if len(out) == 0 {
		b, ferr := os.ReadFile("/proc/net/if_inet6")
		if ferr == nil {
			for _, line := range strings.Split(string(b), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 6 {
					continue
				}
				raw, err := hex.DecodeString(fields[0])
				if err != nil || len(raw) != net.IPv6len {
					continue
				}
				ip := net.IP(raw)
				if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
					continue
				}
				name := fields[5]
				s := ip.String()
				if ip.IsLinkLocalUnicast() && name != "" {
					s += "%" + name
				}
				if !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
	}
	return out
}
