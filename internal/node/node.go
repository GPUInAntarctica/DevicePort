// Package node implements the DevicePort overlay engine: it owns the virtual
// TUN device, the encrypted UDP data plane, NAT hole punching, relay fallback
// and the signaling control connection.
package node

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
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
)

// Options configures a Node.
type Options struct {
	Server  string
	Network string
	Name    string
	Logger  *log.Logger
	// AutoConnect causes every discovered peer in the network to be punched
	// automatically (the default virtual-LAN behavior).
	AutoConnect bool
}

// Peer holds the runtime state of one remote node.
type Peer struct {
	Info      protocol.PeerInfo
	Key       []byte
	Endpoints []string

	direct     bool
	activeEP   string
	punchUntil time.Time
	lastTry    time.Time
	lastSeen   time.Time
	tx, rx     uint64
}

// PeerStatus is a snapshot for the GUI/CLI.
type PeerStatus struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	Direct   bool      `json:"direct"`
	Endpoint string    `json:"endpoint"`
	Mode     string    `json:"mode"`
	TX       uint64    `json:"tx"`
	RX       uint64    `json:"rx"`
	LastSeen time.Time `json:"last_seen"`
}

// Status is a snapshot of the whole node for the GUI/CLI.
type Status struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	IP         string       `json:"ip"`
	Prefix     string       `json:"prefix"`
	Network    string       `json:"network"`
	Server     string       `json:"server"`
	UDPAddr    string       `json:"udp_addr"`
	Encryption string       `json:"encryption"`
	Uptime     string       `json:"uptime"`
	Peers      []PeerStatus `json:"peers"`
}

// Node is a DevicePort client instance.
type Node struct {
	ident  *crypto.Identity
	dev    tun.Device
	opts   Options
	logger *log.Logger

	udp       *net.UDPConn
	control   net.Conn
	controlMu sync.Mutex

	mu        sync.Mutex
	peers     map[string]*Peer
	byIP      map[string]*Peer
	byShortID map[string]*Peer
	selfIP    string
	prefix    string
	started   time.Time

	closed   chan struct{}
	closeOne sync.Once
	wg       sync.WaitGroup
	running  atomic.Bool

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
		closed:    make(chan struct{}),
		started:   time.Now(),
	}, nil
}

// UDPAddr returns the bound UDP address (useful for tests).
func (n *Node) UDPAddr() net.Addr { return n.udp.LocalAddr() }

// Start connects to the signaling server and launches all worker loops.
func (n *Node) Start() error {
	conn, err := net.DialTimeout("tcp", n.opts.Server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect signaling server %s: %w", n.opts.Server, err)
	}
	n.control = conn
	hello := &protocol.Message{
		Type:      protocol.MsgHello,
		ID:        n.ident.ID(),
		Name:      n.opts.Name,
		PublicKey: n.ident.PublicKey(),
		Network:   n.opts.Network,
		Port:      n.udp.LocalAddr().(*net.UDPAddr).Port,
		Endpoints: localEndpoints(n.udp.LocalAddr().(*net.UDPAddr).Port),
	}
	if err := protocol.WriteMessage(conn, hello); err != nil {
		conn.Close()
		return fmt.Errorf("send hello: %w", err)
	}

	n.running.Store(true)
	n.wg.Add(4)
	go n.controlLoop()
	go n.udpLoop()
	go n.tunLoop()
	go n.maintenanceLoop()
	n.logger.Printf("node %s started (udp %s, server %s)", short(n.ident.ID()), n.udp.LocalAddr(), n.opts.Server)
	return nil
}

// Close stops all loops and releases resources.
func (n *Node) Close() error {
	var err error
	n.closeOne.Do(func() {
		n.running.Store(false)
		close(n.closed)
		if n.control != nil {
			n.control.Close()
		}
		if n.udp != nil {
			n.udp.Close()
		}
		if n.dev != nil {
			err = n.dev.Close()
		}
		n.wg.Wait()
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
			n.mu.Unlock()
			n.logger.Printf("assigned virtual IP %s (%s)", m.YourIP, m.Prefix)
			if n.OnAddress != nil {
				if err := n.OnAddress(m.YourIP, m.Prefix); err != nil {
					n.logger.Printf("configure tun address: %v", err)
				}
			}
			for i := range m.Peers {
				n.learnPeer(&m.Peers[i])
			}
			n.notify()
		case protocol.MsgPeer:
			if m.Peer != nil {
				n.learnPeer(m.Peer)
				n.notify()
			}
		case protocol.MsgPeerLeft:
			n.forgetPeer(m.ID)
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
	if p == nil {
		now := time.Now()
		p = &Peer{lastTry: now, punchUntil: now.Add(punchBurst)}
		n.peers[info.ID] = p
		n.byShortID[sid] = p
	}
	oldIP := p.Info.IP
	p.Info = *info
	p.Key = key
	p.Endpoints = append([]string(nil), info.Endpoints...)
	if oldIP != "" && oldIP != info.IP {
		delete(n.byIP, oldIP)
	}
	if info.IP != "" {
		n.byIP[info.IP] = p
	}
	if !p.direct && time.Since(p.lastTry) > punchRetry {
		p.punchUntil = time.Now().Add(punchBurst)
		p.lastTry = time.Now()
	}
	n.mu.Unlock()
	n.logger.Printf("peer %s (%s) at %s, %d endpoint(s)", short(info.ID), info.Name, info.IP, len(info.Endpoints))
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
		nb, src, err := n.udp.ReadFromUDP(buf)
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
	shortID, nonce, ciphertext, frameRelayed, err := protocol.ParseFrame(frame)
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

	atomic.AddUint64(&p.rx, 1)
	n.mu.Lock()
	p.lastSeen = time.Now()
	if src != nil && !frameRelayed {
		// Any valid direct packet confirms the path.
		p.activeEP = src.String()
		p.direct = true
	}
	n.mu.Unlock()

	switch plain[0] {
	case protocol.TypeData:
		if len(plain) > 1 {
			if _, err := n.dev.Write(plain[1:]); err != nil {
				n.logger.Printf("tun write: %v", err)
			}
		}
	case protocol.TypePunch:
		// A direct punch proves bidirectional reachability and stops the
		// burst. Relayed punches do not count as a direct path.
		if src != nil && !frameRelayed {
			n.mu.Lock()
			p.activeEP = src.String()
			p.direct = true
			p.punchUntil = time.Time{}
			n.mu.Unlock()
			n.notify()
		}
	case protocol.TypeKeepalive:
		// no-op: lastSeen already updated
	}
}

// tunLoop reads raw IPv4 packets from the virtual interface and forwards them
// to the peer that owns the destination address.
func (n *Node) tunLoop() {
	defer n.wg.Done()
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
		dst := net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19]).String()
		n.mu.Lock()
		p := n.byIP[dst]
		n.mu.Unlock()
		if p == nil {
			continue // not a peer address, leave to the local stack
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
	direct := p.direct && p.activeEP != ""
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

	// Build the header first; it is the GCM additional authenticated data.
	flags := byte(0)
	if relayed {
		flags = protocol.FlagRelayed
	}
	header := make([]byte, protocol.HeaderSize)
	header[0], header[1], header[2], header[3] = protocol.Magic0, protocol.Magic1, protocol.Version, flags
	copy(header[4:12], sid)

	ct, err := crypto.Seal(p.Key, nonce, plain, header)
	if err != nil {
		return err
	}
	frame, err := protocol.BuildFrame(sid, nonce, ct, relayed)
	if err != nil {
		return err
	}

	if !relayed {
		addr, err := net.ResolveUDPAddr("udp", ep)
		if err != nil {
			return err
		}
		_, err = n.udp.WriteToUDP(frame, addr)
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
		_ = n.sendToPeer(p, protocol.TypeKeepalive, nil)
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
		if p.direct {
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
			_ = n.sendFrame(t.peer, protocol.TypePunch, nil, ep, false)
		}
	}
}

// Status returns a thread-safe snapshot for the API.
func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := Status{
		ID:         n.ident.ID(),
		Name:       n.opts.Name,
		IP:         n.selfIP,
		Prefix:     n.prefix,
		Network:    n.opts.Network,
		Server:     n.opts.Server,
		UDPAddr:    n.udp.LocalAddr().String(),
		Encryption: "X25519 + HKDF-SHA256 + AES-256-GCM",
	}
	if n.running.Load() {
		s.Uptime = time.Since(n.started).Truncate(time.Second).String()
	}
	for _, p := range n.peers {
		mode := "relay"
		if p.direct {
			mode = "direct"
		}
		s.Peers = append(s.Peers, PeerStatus{
			ID: p.Info.ID, Name: p.Info.Name, IP: p.Info.IP,
			Direct: p.direct, Endpoint: p.activeEP, Mode: mode,
			TX: atomic.LoadUint64(&p.tx), RX: atomic.LoadUint64(&p.rx),
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
