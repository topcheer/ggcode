// Package platform implements ggcode's self-hosted agent platform server:
// a small multi-user, JWT-authenticated HTTP control plane that submits
// headless agent jobs ("background agents") bound to whitelisted workspace
// directories. User code never leaves the machine running `ggcode serve`.
package platform

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims is the JWT payload issued by the platform server.
type Claims struct {
	Sub string `json:"sub"` // username
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// DefaultTokenTTL is how long an issued access token stays valid.
const DefaultTokenTTL = 24 * time.Hour

var (
	// ErrBadToken is returned for any malformed, tampered, wrong-key or
	// expired token. A single sentinel keeps error wording stable.
	ErrBadToken = errors.New("platform: invalid or expired token")
)

const jwtHeader = "{\"alg\":\"HS256\",\"typ\":\"JWT\"}"

// SignToken issues an HS256 JWT for sub with the given lifetime.
func SignToken(secret []byte, sub string, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("platform: empty signing secret")
	}
	if sub == "" {
		return "", errors.New("platform: empty token subject")
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	now := time.Now().UTC()
	claims := Claims{Sub: sub, Iat: now.Unix(), Exp: now.Add(ttl).Unix()}
	return signClaims(secret, claims)
}

func signClaims(secret []byte, claims Claims) (string, error) {
	hey, err := b64([]byte(jwtHeader))
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	pay, err := b64(body)
	if err != nil {
		return "", err
	}
	sig := hmacSHA256(secret, hey+"."+pay)
	return hey + "." + pay + "." + b64Raw(sig), nil
}

// VerifyToken validates signature, algorithm and expiry. It returns the
// claims on success and ErrBadToken for every failure mode (uniform error,
// constant-response surface for callers).
func VerifyToken(secret []byte, token string) (*Claims, error) {
	if len(secret) == 0 || token == "" {
		return nil, ErrBadToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrBadToken
	}
	hey, pay, sigB64 := parts[0], parts[1], parts[2]
	// Reject non-HS256 alg headers (alg-confusion defense); the header must
	// be byte-identical to what SignToken emits.
	heyJSON, err := b64Decode(hey)
	if err != nil || string(heyJSON) != jwtHeader {
		return nil, ErrBadToken
	}
	want := hmacSHA256(secret, hey+"."+pay)
	got, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !hmac.Equal(want, got) {
		return nil, ErrBadToken
	}
	var claims Claims
	body, err := b64Decode(pay)
	if err != nil || json.Unmarshal(body, &claims) != nil {
		return nil, ErrBadToken
	}
	if claims.Sub == "" || time.Now().Unix() >= claims.Exp {
		return nil, ErrBadToken
	}
	return &claims, nil
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func b64(b []byte) (string, error) {
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func b64Raw(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomID returns a 16-hex-char random identifier (collision odds are
// negligible for single-server job counts).
func RandomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("platform: random id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}
