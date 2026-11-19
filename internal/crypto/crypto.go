// Package crypto implements DevicePort's identity, key agreement and
// authenticated encryption. It deliberately uses only the Go standard library
// so that it cross-compiles cleanly to Linux, Windows and Android without cgo.
//
// Design:
//   - Each node owns a static X25519 key pair. The fingerprint of the public
//     key (SHA-256, first 16 bytes) is the node's globally unique ID.
//   - Two peers derive a symmetric key with X25519 ECDH followed by
//     HKDF-SHA256 (info = "deviceport-v1"). The signaling/relay server never
//     sees this key, so relay traffic stays end-to-end encrypted.
//   - Packets are sealed with AES-256-GCM using a fresh random 96-bit nonce
//     per packet. The 12-byte wire header is used as additional authenticated
//     data, so an attacker cannot tamper with routing metadata either.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	PrivateKeySize = 32
	PublicKeySize  = 32
	KeySize        = 32 // AES-256
	NonceSize      = 12 // GCM standard nonce
	IDBytes        = 16 // truncated SHA-256 of public key
)

var hkdfInfo = []byte("deviceport-v1/overlay-key")

// Identity is a node's long-term X25519 key pair.
type Identity struct {
	priv *ecdh.PrivateKey
	pub  []byte
	id   string
}

// GenerateIdentity creates a fresh random identity.
func GenerateIdentity() (*Identity, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	pub := priv.PublicKey().Bytes()
	return &Identity{
		priv: priv,
		pub:  append([]byte(nil), pub...),
		id:   NodeID(pub),
	}, nil
}

// FromPrivate reconstructs an identity from 32 raw private-key bytes.
func FromPrivate(private []byte) (*Identity, error) {
	priv, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, err
	}
	pub := priv.PublicKey().Bytes()
	return &Identity{
		priv: priv,
		pub:  append([]byte(nil), pub...),
		id:   NodeID(pub),
	}, nil
}

// PublicKey returns a copy of the X25519 public key (32 bytes).
func (i *Identity) PublicKey() []byte { return append([]byte(nil), i.pub...) }

// PrivateKey returns a copy of the raw private key (32 bytes) for persistence.
func (i *Identity) PrivateKey() []byte { return append([]byte(nil), i.priv.Bytes()...) }

// ID returns the node's stable hexadecimal identifier.
func (i *Identity) ID() string { return i.id }

// NodeID computes the 32-hex-character ID from a public key.
func NodeID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:IDBytes])
}

// ShortID is the 8-byte binary prefix placed on the wire to identify a sender.
func ShortID(id string) ([]byte, error) {
	b, err := hex.DecodeString(id)
	if err != nil || len(b) < 8 {
		return nil, errors.New("invalid node id")
	}
	return b[:8], nil
}

// SharedKey performs X25519 ECDH between our private key and their public key,
// then stretches the result with HKDF-SHA256 into a 32-byte symmetric key.
func SharedKey(myPriv, theirPub []byte) ([]byte, error) {
	priv, err := ecdh.X25519().NewPrivateKey(myPriv)
	if err != nil {
		return nil, fmt.Errorf("bad private key: %w", err)
	}
	pub, err := ecdh.X25519().NewPublicKey(theirPub)
	if err != nil {
		return nil, fmt.Errorf("bad peer public key: %w", err)
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("ecdh failed: %w", err)
	}
	key := HKDF(shared, nil, hkdfInfo, KeySize)
	return key, nil
}

// HKDF implements HKDF-SHA256 (RFC 5869) with the given salt and info.
func HKDF(secret, salt, info []byte, length int) []byte {
	if salt == nil {
		salt = make([]byte, sha256.Size)
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write(secret)
	prk := mac.Sum(nil)

	var out []byte
	var t []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac = hmac.New(sha256.New, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{counter})
		t = mac.Sum(nil)
		out = append(out, t...)
	}
	return out[:length]
}

// Seal encrypts plaintext with AES-256-GCM. The caller must supply a unique
// 12-byte nonce for every packet under a given key.
func Seal(key, nonce, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("nonce must be %d bytes", NonceSize)
	}
	return gcm.Seal(nil, nonce, plaintext, aad), nil
}

// Open decrypts and authenticates a packet produced by Seal.
func Open(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("nonce must be %d bytes", NonceSize)
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

// RandomNonce returns a cryptographically random 96-bit nonce.
func RandomNonce() ([]byte, error) {
	b := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}
