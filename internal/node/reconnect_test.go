package node

import (
	"testing"
	"time"

	"github.com/GPUInAntarctica/DevicePort/internal/crypto"
	"github.com/GPUInAntarctica/DevicePort/internal/tun"
)

// Reconnect must switch the controller/network in place, keeping the same
// TUN device and node identity, so the web GUI can join another network.
func TestReconnectSwitchesNetwork(t *testing.T) {
	addr := startTestServer(t)
	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dev := tun.NewUserspaceTun("dp-reconnect", 1400)
	n, err := New(id, dev, Options{Server: addr, Network: "first", Name: "switcher", Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return n.Status().IP != "" }, "initial IP assignment")

	if err := n.Reconnect(addr, "second"); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		s := n.Status()
		return s.Network == "second" && s.IP != "" && s.Uptime != ""
	}, "new network assignment")
	if got := n.Status().Network; got != "second" {
		t.Fatalf("network = %q, want second", got)
	}
}
