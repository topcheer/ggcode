package a2a

// Agent Card JWS signature verification (A2A spec §8.4 "Agent Card Signing").
//
// The publisher signs the RFC 8785 (JCS) canonicalized Agent Card JSON —
// excluding the signatures field itself — and attaches AgentCardSignature
// blocks to the card. This verifier reproduces the canonical payload, fetches
// the signing key from an embedded JWK or the jku JWKS URL, and checks the
// JWS signature per RFC 7515.
//
// Policy (fail-closed on tampering):
//   - a signature whose crypto check fails with a known algorithm marks the
//     card as tampered and discovery refuses to proceed;
//   - unsupported algorithms (e.g. HS256 symmetric) are reported but never
//     count as tampering — a card may legitimately carry signatures this
//     build cannot validate;
//   - when no verifiable key is obtainable the card is used unverified, the
//     same behavior as clients without this capability, with a debug log.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"
)

// AgentCardSignature is one JWS (RFC 7515) signature block attached to an
// Agent Card (A2A spec §8.4).
type AgentCardSignature struct {
	Protected string            `json:"protected"`
	Signature string            `json:"signature"`
	Header    map[string]string `json:"header,omitempty"`
}

// CardSigStatus classifies the outcome of verifying one signature.
type CardSigStatus string

const (
	CardSigVerified    CardSigStatus = "verified"
	CardSigInvalid     CardSigStatus = "invalid"
	CardSigUnsupported CardSigStatus = "unsupported-alg"
	CardSigNoKey       CardSigStatus = "no-key"
)

// CardSignatureResult is the per-signature verification outcome.
type CardSignatureResult struct {
	Index  int
	Alg    string
	KID    string
	Status CardSigStatus
	Err    error
}

// CardTamperedError is returned when a card's JWS signature fails to verify,
// proving the card (or its signature) was modified in transit.
type CardTamperedError struct {
	Index int
	Alg   string
}

func (e *CardTamperedError) Error() string {
	return fmt.Sprintf("a2a: agent card signature %d (alg %s) does not verify - card may be tampered", e.Index, e.Alg)
}

const jwksFetchTimeout = 5 * time.Second

// cardMaxBodyBytes caps how much of a card response body the client reads
// before signature verification (cards are small; anything larger is abuse).
const cardMaxBodyBytes = 1 << 20

// hasCardSignatures reports whether a raw card JSON object carries a
// non-empty signatures field.
func hasCardSignatures(raw json.RawMessage) bool {
	var m struct {
		Signatures []json.RawMessage `json:"signatures"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	return len(m.Signatures) > 0
}

// jwksFetcher is injectable for tests; production uses fetchJWKS.
type jwksFetcher func(ctx context.Context, jku string) (map[string]json.RawMessage, error)

// VerifyAgentCardSignature verifies all JWS signatures on a raw Agent Card
// (A2A spec §8.4). rawCard is the card JSON exactly as received; the signed
// payload is its RFC 8785 canonicalization with the signatures field removed.
//
// Returns per-signature results. The second return value is true when at
// least one signature with a supported algorithm failed cryptographically —
// callers must treat that as tampering and reject the card.
func VerifyAgentCardSignature(rawCard []byte) ([]CardSignatureResult, bool) {
	return verifyAgentCardSignature(context.Background(), rawCard, fetchJWKS)
}

func verifyAgentCardSignature(ctx context.Context, rawCard []byte, fetcher jwksFetcher) ([]CardSignatureResult, bool) {
	var cardMap map[string]json.RawMessage
	if err := json.Unmarshal(rawCard, &cardMap); err != nil {
		// Caller decoded the same bytes into AgentCard already, so this is
		// not reachable in practice; treat as tampered to stay fail-closed.
		return []CardSignatureResult{{Status: CardSigInvalid, Err: err}}, true
	}
	sigsJSON, ok := cardMap["signatures"]
	if !ok {
		return nil, false
	}
	var sigs []AgentCardSignature
	if err := json.Unmarshal(sigsJSON, &sigs); err != nil {
		return []CardSignatureResult{{Status: CardSigInvalid, Err: fmt.Errorf("decode signatures: %w", err)}}, true
	}
	if len(sigs) == 0 {
		return nil, false
	}

	// Signed payload: canonicalized card WITHOUT the signatures field
	// (A2A §8.4: exclusion avoids the circular-dependency problem).
	delete(cardMap, "signatures")
	stripped, err := json.Marshal(cardMap)
	if err != nil {
		return []CardSignatureResult{{Status: CardSigInvalid, Err: err}}, true
	}
	payload, err := jcsCanonicalize(stripped)
	if err != nil {
		return []CardSignatureResult{{Status: CardSigInvalid, Err: err}}, true
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)

	results := make([]CardSignatureResult, 0, len(sigs))
	tampered := false
	for i := range sigs {
		res := verifyOneCardSignature(ctx, i, &sigs[i], payloadB64, fetcher)
		results = append(results, res)
		if res.Status == CardSigInvalid {
			tampered = true
		}
	}
	return results, tampered
}

func verifyOneCardSignature(ctx context.Context, idx int, sig *AgentCardSignature, payloadB64 string, fetcher jwksFetcher) CardSignatureResult {
	res := CardSignatureResult{Index: idx}
	if sig.Protected == "" || sig.Signature == "" {
		res.Status = CardSigInvalid
		res.Err = fmt.Errorf("missing protected header or signature")
		return res
	}
	protBytes, err := base64.RawURLEncoding.DecodeString(sig.Protected)
	if err != nil {
		res.Status = CardSigInvalid
		res.Err = fmt.Errorf("decode protected header: %w", err)
		return res
	}
	var prot struct {
		Alg string          `json:"alg"`
		KID string          `json:"kid"`
		JKU string          `json:"jku"`
		JWK json.RawMessage `json:"jwk"`
	}
	if err := json.Unmarshal(protBytes, &prot); err != nil {
		res.Status = CardSigInvalid
		res.Err = fmt.Errorf("decode protected header JSON: %w", err)
		return res
	}
	res.Alg = prot.Alg
	res.KID = prot.KID

	sigBytes, err := base64.RawURLEncoding.DecodeString(sig.Signature)
	if err != nil {
		res.Status = CardSigInvalid
		res.Err = fmt.Errorf("decode signature: %w", err)
		return res
	}
	signingInput := []byte(sig.Protected + "." + payloadB64)

	// Resolve the verification key: embedded JWK first, then jku JWKS URL.
	var jwk json.RawMessage
	switch {
	case len(prot.JWK) > 0:
		jwk = prot.JWK
	case prot.JKU != "":
		set, ferr := fetcher(ctx, prot.JKU)
		if ferr != nil {
			res.Status = CardSigNoKey
			res.Err = fmt.Errorf("fetch jku %q: %w", prot.JKU, ferr)
			return res
		}
		if jwk = matchJWKSKey(set, prot.KID); jwk == nil {
			res.Status = CardSigNoKey
			res.Err = fmt.Errorf("jku JWKS has no key with kid %q", prot.KID)
			return res
		}
	default:
		res.Status = CardSigNoKey
		res.Err = fmt.Errorf("no jwk or jku in protected header")
		return res
	}

	verified, verr := jwsVerify(prot.Alg, jwk, signingInput, sigBytes)
	if verr != nil {
		if _, unsupported := verr.(errUnsupportedAlg); unsupported {
			res.Status = CardSigUnsupported
		} else {
			res.Status = CardSigNoKey
		}
		res.Err = verr
		return res
	}
	if !verified {
		res.Status = CardSigInvalid
		res.Err = fmt.Errorf("signature mismatch")
		return res
	}
	res.Status = CardSigVerified
	return res
}

type errUnsupportedAlg string

func (e errUnsupportedAlg) Error() string { return "unsupported JWS algorithm: " + string(e) }

// jwsVerify verifies signingInput against sigBytes using the JWK. It supports
// ES256, RS256 and EdDSA — the asymmetric algorithms appropriate for
// publisher-signed cards. Symmetric algorithms are deliberately refused.
func jwsVerify(alg string, jwk json.RawMessage, signingInput, sig []byte) (bool, error) {
	digest := sha256.Sum256(signingInput)
	switch alg {
	case "ES256":
		key, err := jwkECDSAP256(jwk)
		if err != nil {
			return false, err
		}
		if len(sig) != 64 {
			return false, fmt.Errorf("ES256 signature must be 64 bytes (r||s), got %d", len(sig))
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(key, digest[:], r, s), nil
	case "RS256":
		key, err := jwkRSA(jwk)
		if err != nil {
			return false, err
		}
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
			return false, nil
		}
		return true, nil
	case "EdDSA":
		key, err := jwkEd25519(jwk)
		if err != nil {
			return false, err
		}
		return ed25519.Verify(key, signingInput, sig), nil
	default:
		return false, errUnsupportedAlg(alg)
	}
}

func b64uField(jwk map[string]string, name string) ([]byte, error) {
	v, ok := jwk[name]
	if !ok || v == "" {
		return nil, fmt.Errorf("jwk missing %q", name)
	}
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("jwk %q: %w", name, err)
	}
	return b, nil
}

func jwkMap(jwk json.RawMessage) (map[string]string, string, error) {
	var m map[string]string
	if err := json.Unmarshal(jwk, &m); err != nil {
		return nil, "", fmt.Errorf("decode jwk: %w", err)
	}
	kty := m["kty"]
	if kty == "" {
		return nil, "", fmt.Errorf("jwk missing kty")
	}
	return m, kty, nil
}

func jwkECDSAP256(jwk json.RawMessage) (*ecdsa.PublicKey, error) {
	m, kty, err := jwkMap(jwk)
	if err != nil {
		return nil, err
	}
	if kty != "EC" || m["crv"] != "P-256" {
		return nil, fmt.Errorf("ES256 requires EC P-256 jwk, got kty=%q crv=%q", kty, m["crv"])
	}
	x, err := b64uField(m, "x")
	if err != nil {
		return nil, err
	}
	y, err := b64uField(m, "y")
	if err != nil {
		return nil, err
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
}

func jwkRSA(jwk json.RawMessage) (*rsa.PublicKey, error) {
	m, kty, err := jwkMap(jwk)
	if err != nil {
		return nil, err
	}
	if kty != "RSA" {
		return nil, fmt.Errorf("RS256 requires RSA jwk, got kty=%q", kty)
	}
	n, err := b64uField(m, "n")
	if err != nil {
		return nil, err
	}
	e, err := b64uField(m, "e")
	if err != nil {
		return nil, err
	}
	if len(e) == 0 || len(e) > 8 {
		return nil, fmt.Errorf("jwk exponent out of range")
	}
	eInt := new(big.Int).SetBytes(e)
	if !eInt.IsInt64() || eInt.Int64() < 3 {
		return nil, fmt.Errorf("jwk exponent invalid")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(eInt.Int64())}, nil
}

func jwkEd25519(jwk json.RawMessage) (ed25519.PublicKey, error) {
	m, kty, err := jwkMap(jwk)
	if err != nil {
		return nil, err
	}
	if kty != "OKP" || m["crv"] != "Ed25519" {
		return nil, fmt.Errorf("EdDSA requires OKP Ed25519 jwk, got kty=%q crv=%q", kty, m["crv"])
	}
	k, err := b64uField(m, "x")
	if err != nil {
		return nil, err
	}
	if len(k) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("Ed25519 public key must be %d bytes, got %d", ed25519.PublicKeySize, len(k))
	}
	return ed25519.PublicKey(k), nil
}

// matchJWKSKey picks the keys entry matching kid (empty kid matches the
// single entry when the set has exactly one key).
func matchJWKSKey(set map[string]json.RawMessage, kid string) json.RawMessage {
	if set == nil {
		return nil
	}
	var keys []json.RawMessage
	if k, ok := set["keys"]; ok {
		_ = json.Unmarshal(k, &keys)
	}
	if len(keys) == 0 {
		return nil
	}
	if kid == "" {
		if len(keys) == 1 {
			return keys[0]
		}
		return nil
	}
	for _, k := range keys {
		var m struct {
			KID string `json:"kid"`
		}
		if err := json.Unmarshal(k, &m); err == nil && subtle.ConstantTimeCompare([]byte(m.KID), []byte(kid)) == 1 {
			return k
		}
	}
	return nil
}

// fetchJWKS downloads a JWKS from the jku URL. Only https (plus loopback
// plain http for local development and tests) is accepted; a card publisher
// must not direct key lookups to arbitrary plaintext origins.
func fetchJWKS(ctx context.Context, jku string) (map[string]json.RawMessage, error) {
	u, err := parseSecureHTTP(jku)
	if err != nil {
		return nil, err
	}
	c := &http.Client{Timeout: jwksFetchTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks fetch: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("jwks read: %w", err)
	}
	var set map[string]json.RawMessage
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("jwks decode: %w", err)
	}
	return set, nil
}

func parseSecureHTTP(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	secure := strings.HasPrefix(raw, "https://")
	if !secure && !strings.HasPrefix(raw, "http://") {
		return "", fmt.Errorf("jku %q: unsupported scheme", raw)
	}
	if !secure {
		host := raw[len("http://"):]
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			// loopback http allowed (local dev / tests)
		} else if host == "localhost" {
			// allowed
		} else {
			return "", fmt.Errorf("jku %q: plain http only allowed for loopback", raw)
		}
	}
	return raw, nil
}
