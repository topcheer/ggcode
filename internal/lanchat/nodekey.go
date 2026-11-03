package lanchat

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

type NodeKey struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

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

func KeyFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "fp:" + hex.EncodeToString(sum[:16])
}

func canonicalPayload(m *Message) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d",
		m.ID, m.FromNodeID, m.FromRole, m.FromNick, m.ToNodeID, m.ToRole, m.Content, m.Timestamp))
}

func signMessage(m *Message, key *NodeKey) {
	if key == nil {
		return
	}
	m.PubKey = hex.EncodeToString(key.Public)
	m.Sig = hex.EncodeToString(ed25519.Sign(key.Private, canonicalPayload(m)))
}

var errNoSignature = errors.New("lanchat: message carries no signature")

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
