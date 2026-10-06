package lanchat

// #3399/#3402 (A-track, TUFU): DM-layer Ed25519 signatures with
// trust-on-first-use identity pinning. Each node holds a static keypair;
// every outbound message carries the public key plus a signature over a
// canonical payload. Receivers verify the signature and pin
// node_id -> public key on first sight; a later claim under the same
// node_id with a different key is rejected as unverified. The strict-mode
// trusted-peer exemption additionally requires the message to be
// signature-verified AND the sender's key fingerprint to match the one
// bound at trust time - a forged from_node_id (plain body field) no
// longer reaches the exemption (#3399 attack chain step 2).

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
)

// NodeKey is this node's static identity keypair.
type NodeKey struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// LoadOrCreateNodeKey loads <dir>/node-ed25519.key or generates and
// persists a new keypair (0600) on first run.
func LoadOrCreateNodeKey(dir string) (*NodeKey, error) {
	path := filepath.Join(dir, "node-ed25519.key")
	if raw, err := os.ReadFile(path); err == nil {
		if len(raw) == ed25519.PrivateKeySize {
			priv := ed25519.PrivateKey(raw)
			return &NodeKey{Private: priv, Public: priv.Public().(ed25519.PublicKey)}, nil
		}
		debug.Log("lanchat", "node key %s has wrong size %d, regenerating", path, len(raw))
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, priv, 0o600); err != nil {
		return nil, err
	}
	return &NodeKey{Private: priv, Public: pub}, nil
}

// KeyFingerprint returns a stable hex fingerprint for a public key.
func KeyFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "fp:" + hex.EncodeToString(sum[:16])
}

// canonicalPayload is the exact byte string covered by message
// signatures. Every identity-bearing and content field participates so a
// forged from_node_id/nick/content breaks the signature. Message.ID is
// included too (#3419): without it, replaying a captured DM under a NEW
// id (dropping the old one's dedup entry) or after a dedup-table restart
// verifies clean - the signature now binds message uniqueness.
func canonicalPayload(m *Message) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d",
		m.ID, m.FromNodeID, m.FromRole, m.FromNick, m.ToNodeID, m.ToRole, m.Content, m.Timestamp))
}

// signMessage attaches PubKey (hex) and Sig (hex) to the message using
// the node key.
func signMessage(m *Message, key *NodeKey) {
	if key == nil {
		return
	}
	m.PubKey = hex.EncodeToString(key.Public)
	m.Sig = hex.EncodeToString(ed25519.Sign(key.Private, canonicalPayload(m)))
}

var errNoSignature = errors.New("lanchat: message carries no signature")

// verifyMessage checks the signature and reports the signer fingerprint.
// Messages without a key/signature (pre-#3402 peers) verify as
// unverified with a nil fingerprint, not as an error - callers decide
// policy; the strict-mode exemption requires a real fingerprint match.
func verifyMessage(m *Message) (ok bool, fp string, err error) {
	if m.PubKey == "" || m.Sig == "" {
		return false, "", errNoSignature
	}
	pubRaw, err := hex.DecodeString(m.PubKey)
	if err != nil || len(pubRaw) != ed25519.PublicKeySize {
		return false, "", fmt.Errorf("lanchat: bad public key")
	}
	sig, err := hex.DecodeString(m.Sig)
	if err != nil {
		return false, "", fmt.Errorf("lanchat: bad signature encoding")
	}
	pub := ed25519.PublicKey(pubRaw)
	if !ed25519.Verify(pub, canonicalPayload(m), sig) {
		return false, "", errors.New("lanchat: signature mismatch")
	}
	return true, KeyFingerprint(pub), nil
}
