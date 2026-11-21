package node

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/GPUInAntarctica/DevicePort/internal/protocol"
)

// LANDiscoveryPort is the IPv6 multicast port used for local peer discovery.
// Announcements are small, unauthenticated hints; the actual data plane still
// uses the X25519-derived symmetric key, so a forged announcement cannot join
// the encrypted network.
const LANDiscoveryPort = 9977

var lanDiscoveryGroup = net.ParseIP("ff02::1") // all IPv6 nodes on the link

const lanAnnounce = "lan-announce"

type lanMessage struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Network   string `json:"network"`
	Port      int    `json:"port"`
	PublicKey []byte `json:"pubkey,omitempty"`
}

type lanSocket struct {
	conn *net.UDPConn
	zone string
}

// startDiscovery joins the IPv6 all-nodes multicast group on every suitable
// interface, announces this node periodically, and introduces peers heard on
// the same local network. It returns an idempotent stop function.
func (n *Node) startDiscovery() func() {
	stop := make(chan struct{})
	var once sync.Once

	ifaces, err := net.Interfaces()
	if err != nil {
		return func() {}
	}
	port := 0
	if a, ok := n.udp.LocalAddr().(*net.UDPAddr); ok {
		port = a.Port
	}
	msg := lanMessage{
		Type:      lanAnnounce,
		ID:        n.ident.ID(),
		Name:      n.opts.Name,
		Network:   n.opts.Network,
		Port:      port,
		PublicKey: n.ident.PublicKey(),
	}
	payload, err := json.Marshal(&msg)
	if err != nil {
		return func() {}
	}

	var sockets []lanSocket
	for i := range ifaces {
		iface := ifaces[i]
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 ||
			iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addr := &net.UDPAddr{IP: lanDiscoveryGroup, Port: LANDiscoveryPort, Zone: iface.Name}
		conn, err := net.ListenMulticastUDP("udp6", &iface, addr)
		if err != nil {
			n.logger.Printf("LAN discovery: join %s on %s failed: %v", lanDiscoveryGroup, iface.Name, err)
			continue
		}
		// Without loopback, a multicast sender cannot hear its own group traffic
		// on some stacks, which also prevents peers on the same host/emulator.
		enableMulticastLoopback(conn)
		sockets = append(sockets, lanSocket{conn: conn, zone: iface.Name})
		go n.readLAN(conn)
	}
	if len(sockets) == 0 {
		return func() {}
	}

	send := func() {
		for _, s := range sockets {
			dst := &net.UDPAddr{IP: lanDiscoveryGroup, Port: LANDiscoveryPort, Zone: s.zone}
			_, _ = s.conn.WriteToUDP(payload, dst)
		}
	}
	go func() {
		send()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				send()
			case <-stop:
				return
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(stop)
			for _, s := range sockets {
				_ = s.conn.Close()
			}
		})
	}
}

func (n *Node) readLAN(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		nr, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var m lanMessage
		if json.Unmarshal(buf[:nr], &m) != nil || m.Type != lanAnnounce {
			continue
		}
		if m.ID == n.ident.ID() || m.Network != n.opts.Network || len(m.PublicKey) == 0 {
			continue
		}
		// The multicast source port is the discovery socket, not the data
		// socket. Combine the observed source address with the announced port.
		host, _, err := net.SplitHostPort(src.String())
		if err != nil || m.Port <= 0 {
			continue
		}
		ep := net.JoinHostPort(host, fmt.Sprintf("%d", m.Port))
		info := &protocol.PeerInfo{
			ID:        m.ID,
			Name:      m.Name,
			PublicKey: m.PublicKey,
			Endpoints: []string{ep},
		}
		n.learnPeer(info)
		n.recomputeLinks()
		n.notify()
	}
}
