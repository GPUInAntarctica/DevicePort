package server

import (
	"io"
	"log"
	"net"
	"testing"
	"time"

	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/protocol"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(log.New(io.Discard, "", 0))
}

func helloFor(t *testing.T, name string) (*crypto.Identity, *protocol.Message) {
	t.Helper()
	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id, &protocol.Message{
		Type:      protocol.MsgHello,
		PublicKey: id.PublicKey(),
		Name:      name,
		Network:   "default",
		Port:      12345,
	}
}

func TestAuthorizationAndStableIP(t *testing.T) {
	srv := testServer(t)
	srv.SetNetworkAuth("default", true)
	addr, err := srv.Listen("[::1]:0")
	if err != nil {
		t.Fatal(err)
	}

	id1, h1 := helloFor(t, "alice")
	c1 := connectTestClient(t, addr.String(), h1)

	// Unauthorized members get a welcome but no peers and no relay.
	w := readTestMessage(t, c1)
	if w.Type != protocol.MsgWelcome || w.Authorized {
		t.Fatalf("expected unauthorized welcome, got %+v", w)
	}
	if len(w.Peers) != 0 {
		t.Fatalf("unauthorized node received %d peers", len(w.Peers))
	}
	if err := srv.Authorize("default", id1.ID(), true); err != nil {
		t.Fatal(err)
	}
	w = readTestMessage(t, c1)
	if w.Type != protocol.MsgWelcome || !w.Authorized {
		t.Fatalf("expected authorized welcome, got %+v", w)
	}

	// Second member sees the first after authorization.
	id2, h2 := helloFor(t, "bob")
	c2 := connectTestClient(t, addr.String(), h2)
	if w := readTestMessage(t, c2); w.Type != protocol.MsgWelcome || w.Authorized {
		t.Fatalf("second node should also be pending: %+v", w)
	}
	if err := srv.Authorize("default", id2.ID(), true); err != nil {
		t.Fatal(err)
	}
	readTestMessage(t, c2) // authorized welcome
	readTestMessage(t, c1) // introduction of bob

	// IPs are stable for a given identity.
	var ip string
	for _, v := range srv.Networks() {
		for _, m := range v.Members {
			if m.ID == id1.ID() {
				ip = m.IP
			}
		}
	}
	if ip == "" {
		t.Fatal("alice has no IP")
	}
	if got := srv.networks["default"].ipFor(id1.ID()); got != ip {
		t.Fatalf("unstable IP: %s != %s", got, ip)
	}

	// Revoking removes the member from introductions.
	if err := srv.Authorize("default", id2.ID(), false); err != nil {
		t.Fatal(err)
	}
	_ = c1.Close()
	_ = c2.Close()
}

func TestManagedRoutesBroadcast(t *testing.T) {
	srv := testServer(t)
	addr, err := srv.Listen("[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	id, h := helloFor(t, "gw")
	c := connectTestClient(t, addr.String(), h)
	readTestMessage(t, c) // welcome (authorized by default)

	if err := srv.AddRoute("default", "192.168.1.0/24", id.ID()); err != nil {
		t.Fatal(err)
	}
	m := readTestMessage(t, c)
	if m.Type != protocol.MsgRoutes || len(m.Routes) != 1 || m.Routes[0].CIDR != "192.168.1.0/24" {
		t.Fatalf("unexpected routes message: %+v", m)
	}
	if err := srv.RemoveRoute("default", "192.168.1.0/24"); err != nil {
		t.Fatal(err)
	}
	if m := readTestMessage(t, c); m.Type != protocol.MsgRoutes || len(m.Routes) != 0 {
		t.Fatalf("route removal not broadcast: %+v", m)
	}
	_ = c.Close()
}

// --- small test helpers ---

func connectTestClient(t *testing.T, addr string, hello *protocol.Message) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteMessage(c, hello); err != nil {
		t.Fatal(err)
	}
	return c
}

func readTestMessage(t *testing.T, c net.Conn) *protocol.Message {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := protocol.ReadMessage(c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
