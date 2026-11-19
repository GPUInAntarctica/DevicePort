package node

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/protocol"
	"github.com/GPUInAntarctica/DevicePort/internal/server"
	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

func testLogger() *log.Logger {
	if os.Getenv("DP_TEST_LOG") != "" {
		return log.New(os.Stderr, "dp: ", log.LstdFlags)
	}
	return log.New(io.Discard, "", 0)
}

func startTestServer(t *testing.T) string {
	t.Helper()
	srv := server.New(testLogger())
	addr, err := srv.Listen("[::1]:0")
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	return addr.String()
}

func newNode(t *testing.T, name, serverAddr string) (*Node, *tun.UserspaceTun) {
	t.Helper()
	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dev := tun.NewUserspaceTun("dp-"+name, 1400)
	n, err := New(id, dev, Options{Server: serverAddr, Network: "test-net", Name: name, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n, dev
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func directCount(n *Node) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, p := range n.peers {
		if p.direct {
			c++
		}
	}
	return c
}

func forceRelay(n *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, p := range n.peers {
		p.direct = false
		p.activeEP = ""
		p.Endpoints = nil
		p.punchUntil = time.Now().Add(time.Hour)
		p.lastTry = time.Now()
	}
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 != 0 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// buildICMP builds a raw IPv4 ICMP echo request from src to dst.
func buildICMP(src, dst string, payload []byte) []byte {
	icmp := make([]byte, 8+len(payload))
	icmp[0] = 8 // echo request
	binary.BigEndian.PutUint16(icmp[4:6], 0x1234)
	copy(icmp[8:], payload)
	c := checksum(icmp)
	binary.BigEndian.PutUint16(icmp[2:4], c)

	pkt := make([]byte, 20+len(icmp))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8], pkt[9] = 64, 1
	copy(pkt[12:16], net.ParseIP(src).To4())
	copy(pkt[16:20], net.ParseIP(dst).To4())
	copy(pkt[20:], icmp)
	c2 := checksum(pkt[:20])
	binary.BigEndian.PutUint16(pkt[10:12], c2)
	return pkt
}

func TestEndToEndDirectAndRelay(t *testing.T) {
	serverAddr := startTestServer(t)
	a, tunA := newNode(t, "A", serverAddr)
	b, tunB := newNode(t, "B", serverAddr)

	waitFor(t, 8*time.Second, func() bool {
		return len(a.Status().Peers) == 1 && len(b.Status().Peers) == 1
	}, "peer introduction")
	waitFor(t, 8*time.Second, func() bool {
		return directCount(a) == 1 && directCount(b) == 1
	}, "direct UDP hole punching")

	ipA, ipB := a.Status().IP, b.Status().IP
	t.Logf("A=%s B=%s mode=%s", ipA, ipB, a.Status().Peers[0].Mode)
	if ipA == "" || ipB == "" || ipA == ipB {
		t.Fatalf("bad virtual IPs: %q %q", ipA, ipB)
	}

	// --- Direct path: inject a packet on A, expect it decrypted on B. ---
	pkt := buildICMP(ipA, ipB, []byte("direct-payload"))
	if err := tunA.Inject(pkt); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-tunB.Outbound():
		if !bytes.Equal(got, pkt) {
			t.Fatalf("direct packet mismatch: got %x want %x", got, pkt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("direct packet did not arrive at B")
	}

	// --- Relay path: disable direct on both sides and verify fallback. ---
	forceRelay(a)
	forceRelay(b)
	waitFor(t, 2*time.Second, func() bool { return a.Status().Peers[0].Mode == "relay" }, "relay mode")

	pkt2 := buildICMP(ipA, ipB, []byte("relay-payload"))
	if err := tunA.Inject(pkt2); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-tunB.Outbound():
		if !bytes.Equal(got, pkt2) {
			t.Fatalf("relay packet mismatch: got %x want %x", got, pkt2)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relayed packet did not arrive at B")
	}
}

func TestEncryptionRejectsTampering(t *testing.T) {
	nonce, err := crypto.RandomNonce()
	if err != nil {
		t.Fatal(err)
	}
	key := crypto.HKDF([]byte("secret"), nil, []byte("test"), 32)
	ct, err := crypto.Seal(key, nonce, []byte("hello"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.Open(key, nonce, ct, []byte("aad")); err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	ct[0] ^= 0xff
	if _, err := crypto.Open(key, nonce, ct, []byte("aad")); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	if _, err := crypto.Open(key, nonce, ct, []byte("bad")); err == nil {
		t.Fatal("wrong AAD was accepted")
	}
}

func TestReplayProtection(t *testing.T) {
	self, _ := crypto.GenerateIdentity()
	peer, _ := crypto.GenerateIdentity()
	dev := tun.NewUserspaceTun("replay", 1400)
	n, err := New(self, dev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.learnPeer(&protocol.PeerInfo{ID: peer.ID(), PublicKey: peer.PublicKey(), IP: "10.66.0.9"})

	key, _ := crypto.SharedKey(self.PrivateKey(), peer.PublicKey())
	shortID, _ := crypto.ShortID(peer.ID())
	plain := []byte{protocol.TypeData, 1, 2, 3}

	buildFrame := func(seq uint64) []byte {
		nonce, _ := crypto.RandomNonce()
		header := make([]byte, protocol.HeaderSize)
		header[0], header[1], header[2] = protocol.Magic0, protocol.Magic1, protocol.Version
		copy(header[4:12], shortID)
		binary.BigEndian.PutUint64(header[protocol.SeqOffset:protocol.NonceOffset], seq)
		ct, err := crypto.Seal(key, nonce, plain, header)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := protocol.BuildFrame(shortID, seq, nonce, ct, false)
		if err != nil {
			t.Fatal(err)
		}
		return frame
	}

	expectData := func(label string) {
		select {
		case got := <-dev.Outbound():
			if !bytes.Equal(got, plain[1:]) {
				t.Fatalf("%s: bad payload %x", label, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: packet was not delivered", label)
		}
	}
	expectDrop := func(label string) {
		select {
		case <-dev.Outbound():
			t.Fatalf("%s: replayed packet was delivered", label)
		case <-time.After(150 * time.Millisecond):
		}
	}

	n.handleFrame(buildFrame(1), nil)
	expectData("seq 1")
	n.handleFrame(buildFrame(1), nil)
	expectDrop("replay of seq 1")
	n.handleFrame(buildFrame(3), nil)
	expectData("seq 3")
	n.handleFrame(buildFrame(2), nil)
	expectData("late seq 2")
	n.handleFrame(buildFrame(2), nil)
	expectDrop("replay of seq 2")
	n.handleFrame(buildFrame(1000), nil)
	expectData("seq 1000 (window jump)")
}

func TestDirectLinkLimit(t *testing.T) {
	self, _ := crypto.GenerateIdentity()
	dev := tun.NewUserspaceTun("links", 1400)
	n, err := New(self, dev, Options{MaxDirect: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	for i := 0; i < 6; i++ {
		peer, _ := crypto.GenerateIdentity()
		n.learnPeer(&protocol.PeerInfo{ID: peer.ID(), PublicKey: peer.PublicKey(), IP: fmt.Sprintf("10.66.0.%d", i+2)})
	}
	n.recomputeLinks()
	n.mu.Lock()
	direct := 0
	for _, p := range n.peers {
		if !p.relayOnly {
			direct++
		}
	}
	n.mu.Unlock()
	if direct != 2 {
		t.Fatalf("expected 2 direct candidates, got %d", direct)
	}
}

func TestSharedKeyAgreement(t *testing.T) {
	a, _ := crypto.GenerateIdentity()
	b, _ := crypto.GenerateIdentity()
	ka, err := crypto.SharedKey(a.PrivateKey(), b.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	kb, err := crypto.SharedKey(b.PrivateKey(), a.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ka, kb) {
		t.Fatal("ECDH keys do not match")
	}
}
