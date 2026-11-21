package server

import "testing"

// A late cleanup from an old connection must not remove the replacement client
// that reconnected with the same identity (e.g. the Android address-probe
// engine closing while the real VPN engine is starting).
func TestLateDisconnectDoesNotRemoveReplacement(t *testing.T) {
	srv := testServer(t)
	n := srv.netFor("default")

	old := &client{id: "same-id", network: "default", ready: true}
	fresh := &client{id: "same-id", network: "default", ready: true}
	srv.clients["same-id"] = fresh
	n.peers["same-id"] = fresh

	srv.remove(old)
	if srv.clients["same-id"] != fresh {
		t.Fatal("late remove() deleted the replacement client")
	}
	if n.peers["same-id"] != fresh {
		t.Fatal("late remove() deleted the replacement peer")
	}
}
