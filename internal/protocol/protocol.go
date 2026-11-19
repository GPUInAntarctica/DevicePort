// Package protocol defines DevicePort's control-plane messages (JSON over a
// length-prefixed TCP connection to the server) and data-plane wire format
// (encrypted UDP datagrams between peers, optionally wrapped by the relay).
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	Magic0       = 0x44 // 'D'
	Magic1       = 0x50 // 'P'
	Version      = 1
	HeaderSize   = 12 // magic(2) + version(1) + flags(1) + senderShortID(8)
	NonceOffset  = HeaderSize
	NonceSize    = 12
	PayloadStart = HeaderSize + NonceSize

	// Packet types carried inside the encrypted payload.
	TypeData      byte = 1 // payload is a raw IPv4 packet
	TypePunch     byte = 2 // UDP hole-punching probe / keepalive
	TypeKeepalive byte = 3

	FlagNone    byte = 0
	FlagRelayed byte = 1 // informational: this frame was relayed by the server

	MaxControlMessage = 1 << 20 // 1 MiB
	MaxPacket         = 65535
)

// Control message type names.
const (
	MsgHello    = "hello"
	MsgWelcome  = "welcome"
	MsgPeer     = "peer"
	MsgPeerLeft = "peer_left"
	MsgConnect  = "connect"
	MsgRelay    = "relay"
	MsgPing     = "ping"
	MsgPong     = "pong"
	MsgError    = "error"
)

// PeerInfo describes a node as seen by the server / another peer.
type PeerInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	PublicKey []byte   `json:"pubkey,omitempty"`
	IP        string   `json:"ip,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"` // IPv6 host:port candidates
}

// Message is the single envelope used for all control messages. Unused fields
// are omitted from JSON, which keeps the protocol compact and easy to extend.
type Message struct {
	Type string `json:"type"`

	// hello / general identity
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	PublicKey []byte   `json:"pubkey,omitempty"`
	Network   string   `json:"network,omitempty"`
	Port      int      `json:"port,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"`

	// welcome
	YourIP  string     `json:"your_ip,omitempty"`
	Prefix  string     `json:"prefix,omitempty"`
	RelayOK bool       `json:"relay_ok,omitempty"`
	Peers   []PeerInfo `json:"peers,omitempty"`

	// peer / peer_left / connect / relay
	Peer   *PeerInfo `json:"peer,omitempty"`
	Target string    `json:"target,omitempty"`
	To     string    `json:"to,omitempty"`
	From   string    `json:"from,omitempty"`
	Data   []byte    `json:"data,omitempty"`

	// error
	Message string `json:"message,omitempty"`
}

// WriteMessage writes one length-prefixed JSON control message.
func WriteMessage(w io.Writer, m *Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > MaxControlMessage {
		return errors.New("control message too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadMessage reads one length-prefixed JSON control message.
func ReadMessage(r io.Reader) (*Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxControlMessage {
		return nil, fmt.Errorf("invalid control message length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// BuildFrame constructs a data-plane frame from its parts. nonce must be 12
// bytes; the returned slice owns its own copy of all inputs.
func BuildFrame(senderShortID []byte, nonce, ciphertext []byte, relayed bool) ([]byte, error) {
	if len(senderShortID) != 8 {
		return nil, errors.New("sender id must be 8 bytes")
	}
	if len(nonce) != NonceSize {
		return nil, errors.New("nonce must be 12 bytes")
	}
	frame := make([]byte, PayloadStart+len(ciphertext))
	frame[0], frame[1] = Magic0, Magic1
	frame[2] = Version
	frame[3] = FlagNone
	if relayed {
		frame[3] = FlagRelayed
	}
	copy(frame[4:12], senderShortID)
	copy(frame[NonceOffset:PayloadStart], nonce)
	copy(frame[PayloadStart:], ciphertext)
	return frame, nil
}

// AAD returns the additional-authenticated-data for a complete frame.
func AAD(frame []byte) ([]byte, error) {
	if len(frame) < HeaderSize {
		return nil, errors.New("frame too short")
	}
	if frame[0] != Magic0 || frame[1] != Magic1 || frame[2] != Version {
		return nil, errors.New("bad frame magic/version")
	}
	return frame[:HeaderSize], nil
}

// ParseFrame validates the header and splits a frame into its components.
func ParseFrame(frame []byte) (shortID, nonce, ciphertext []byte, relayed bool, err error) {
	if len(frame) < PayloadStart {
		return nil, nil, nil, false, errors.New("frame too short")
	}
	if frame[0] != Magic0 || frame[1] != Magic1 || frame[2] != Version {
		return nil, nil, nil, false, errors.New("bad frame magic/version")
	}
	relayed = frame[3]&FlagRelayed != 0
	return frame[4:12], frame[NonceOffset:PayloadStart], frame[PayloadStart:], relayed, nil
}

// EncodeEndpoint normalizes a host:port string into a canonical IPv6 endpoint.
func EncodeEndpoint(ip net.IP, port int) string {
	return net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
}
