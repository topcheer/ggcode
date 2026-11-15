package a2a

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// #5667: credential binding for the declared-workspace trust arm.
//
// Carrier decision (trust-domain protocol ruling for the #5655 residual C):
// the workspace DOMAIN KEY is the credential. It is a 32-byte random secret
// stored under the workspace's git directory (the whole linked-worktree
// family shares one key via the gitdir pointer; a network-shared workspace
// therefore converges on one key across hosts for free), with a
// .ggcode/a2a-domain.key fallback for non-git workspaces.
//
// Negotiation across hosts rides the LAN discovery channel that already
// distributes nick/endpoint: the key ID and key material travel in the
// mDNS TXT record (wkid/wkkey) and the workspace roster file, so a
// same-workspace peer resolves the target's key with zero manual config.
//
// Trust properties (honest scope): the domain key is a workspace-fleet
// credential distributed over plaintext LAN discovery. It removes the
// #5667 defect - cross-workspace introspection gated only on two PUBLIC
// constants (the default API key ggcode-lan-a2a-v1 and the workspace path
// string). A caller must now obtain a per-workspace random credential
// through the workspace's own discovery domain. It is NOT a defense
// against an active LAN attacker who spoofs discovery declarations
// (trust-on-first-use, same class as the rest of the LAN a2a stack).
//
// Proof shape (stateless, no session state machine by design, mirrors the
// #5575 phase-1 HMAC factor):
//
//	X-Ggcode-Workspace-Proof: v1 <keyID>|<unix-ts>|<nonce>|<hex-hmac>
//	hmac = HMAC-SHA256(key, "ggcode-ws-proof|"+method+"|"+keyID+"|"+ts+"|"+nonce)
//
// The timestamp window tolerates LAN clock skew; replay inside the window
// is accepted (identical to the static API-key first gate).

// DomainKeyFile is the key file name inside the resolved git directory
// (never committed; shared across the linked-worktree family).
const DomainKeyFile = "a2a.key"

// WorkspaceDomainKeyFile is the fallback key file name (relative to
// <workspace>/.ggcode) for workspaces without a git directory.
const WorkspaceDomainKeyFile = "a2a-domain.key"

// WorkspaceProofHeader carries the declared-workspace credential.
const WorkspaceProofHeader = "X-Ggcode-Workspace-Proof"

// workspaceProofWindow bounds proof timestamp freshness (LAN clock skew).
const workspaceProofWindow = 10 * time.Minute

const workspaceProofPrefix = "v1 "

// ResolveGitDir returns the real git directory for dir. In a linked
// worktree dir/.git is a pointer file ("gitdir: /path/to/main/.git/wt");
// the domain key must live in the shared main gitdir so the whole family
// trusts one key (#5575 phase-1 semantics, restored by #5667).
func ResolveGitDir(dir string) (string, error) {
	dot := filepath.Join(dir, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return dot, nil
	}
	raw, err := os.ReadFile(dot)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	const p = "gitdir:"
	if !strings.HasPrefix(s, p) {
		return "", errors.New("a2a: malformed .git pointer file")
	}
	gd := strings.TrimSpace(strings.TrimPrefix(s, p))
	if i := strings.LastIndex(gd, "/worktrees/"); i >= 0 {
		gd = gd[:i]
	}
	return gd, nil
}

// LoadOrCreateWorkspaceDomainKey returns the workspace domain key for dir,
// generating a fresh 32-byte key on first use. Resolution order: the git
// family directory (worktrees share the main gitdir), then
// <workspace>/.ggcode for non-git workspaces.
func LoadOrCreateWorkspaceDomainKey(dir string) ([]byte, error) {
	if gd, err := ResolveGitDir(dir); err == nil {
		return loadOrCreateKeyFile(filepath.Join(gd, DomainKeyFile), false)
	}
	return loadOrCreateKeyFile(filepath.Join(dir, ".ggcode", WorkspaceDomainKeyFile), true)
}

func loadOrCreateKeyFile(path string, mkdir bool) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		if key, kerr := DecodeDomainKey(strings.TrimSpace(string(b))); kerr == nil {
			return key, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	if mkdir {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(raw)), 0o600); err != nil {
		return nil, err
	}
	return raw, nil
}

// DecodeDomainKey parses the 64-hex advertisement form back into key
// bytes (the roster/TXT wire form).
func DecodeDomainKey(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(s)))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("a2a: bad workspace domain key (want 64 hex chars)")
	}
	return b, nil
}

// WorkspaceDomainKeyID derives the public key ID (16 hex chars) used in
// advertisements and proofs. Public data: reveals nothing about the key.
func WorkspaceDomainKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// EncodeDomainKey renders key bytes as the 64-hex wire form.
func EncodeDomainKey(key []byte) string { return hex.EncodeToString(key) }

func workspaceProofMAC(key []byte, method, keyID, ts, nonce string) []byte {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "ggcode-ws-proof|%s|%s|%s|%s", method, keyID, ts, nonce)
	return mac.Sum(nil)
}

// BuildWorkspaceProof mints a stateless possession proof for method under
// key. The timestamp comes from now (caller-injectable for tests).
func BuildWorkspaceProof(key []byte, method string, now time.Time) (string, error) {
	if len(key) != 32 {
		return "", errors.New("a2a: no workspace domain key")
	}
	keyID := WorkspaceDomainKeyID(key)
	ts := strconv.FormatInt(now.Unix(), 10)
	nonceRaw := make([]byte, 8)
	if _, err := rand.Read(nonceRaw); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceRaw)
	sig := hex.EncodeToString(workspaceProofMAC(key, method, keyID, ts, nonce))
	return workspaceProofPrefix + keyID + "|" + ts + "|" + nonce + "|" + sig, nil
}

// VerifyWorkspaceProof checks a proof header against key in constant
// time. A missing/malformed proof, a foreign key ID, a stale timestamp or
// a wrong MAC all fail closed (the declared arm then falls through to the
// roster reverse-lookup arm).
func VerifyWorkspaceProof(key []byte, method, header string, now time.Time) bool {
	if len(key) != 32 || !strings.HasPrefix(header, workspaceProofPrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(header, workspaceProofPrefix), "|")
	if len(parts) != 4 {
		return false
	}
	keyID, ts, nonce, sigHex := parts[0], parts[1], parts[2], parts[3]
	if keyID != WorkspaceDomainKeyID(key) {
		return false
	}
	tsUnix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	skew := now.Unix() - tsUnix
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(workspaceProofWindow.Seconds()) {
		return false
	}
	got, err := hex.DecodeString(strings.ToLower(sigHex))
	if err != nil {
		return false
	}
	want := workspaceProofMAC(key, method, keyID, ts, nonce)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DomainKeyForEndpoint resolves the domain key for a peer endpoint from a
// discovered roster (mDNS or warm roster file entries carry the key
// advertisement). Returns nil when no entry carries a key - the caller
// then proceeds without a proof (server falls back to its roster arm).
func DomainKeyForEndpoint(instances []InstanceInfo, endpoint string) []byte {
	ep := normEndpoint(endpoint)
	for _, inst := range instances {
		if inst.DomainKey == "" || normEndpoint(inst.Endpoint) != ep {
			continue
		}
		if key, err := DecodeDomainKey(inst.DomainKey); err == nil {
			return key
		}
	}
	return nil
}
