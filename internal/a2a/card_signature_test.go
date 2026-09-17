package a2a

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// signCard produces the card JSON with one JWS signature (A2A §8.4 shape).
// jwkMap is embedded into the protected header when non-nil; jku is set when
// non-empty.
func signCard(t *testing.T, card map[string]interface{}, kid, alg string, key interface{}, jwkMap map[string]string, jku string) []byte {
	t.Helper()
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "signatures")
	stripped, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jcsCanonicalize(stripped)
	if err != nil {
		t.Fatal(err)
	}
	prot := map[string]interface{}{"alg": alg, "kid": kid}
	if jwkMap != nil {
		prot["jwk"] = jwkMap
	}
	if jku != "" {
		prot["jku"] = jku
	}
	protJSON, err := json.Marshal(prot)
	if err != nil {
		t.Fatal(err)
	}
	protB64 := b64u(protJSON)
	signingInput := []byte(protB64 + "." + b64u(payload))
	digest := sha256.Sum256(signingInput)

	var sig []byte
	if alg == "HS256" {
		// Symmetric alg never actually signed here; verifier must reject it
		// as unsupported before the bytes matter.
		sig = []byte("garbage-signature-bytes")
	} else {
		switch k := key.(type) {
		case *ecdsa.PrivateKey:
			r, s, err := ecdsa.Sign(rand.Reader, k, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			sig = make([]byte, 64)
			r.FillBytes(sig[:32])
			s.FillBytes(sig[32:])
		case ed25519.PrivateKey:
			sig = ed25519.Sign(k, signingInput)
		case *rsa.PrivateKey:
			sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported test key type %T", key)
		}
	}

	var cm map[string]interface{}
	if err := json.Unmarshal(raw, &cm); err != nil {
		t.Fatal(err)
	}
	cm["signatures"] = []map[string]interface{}{{
		"protected": protB64,
		"signature": b64u(sig),
	}}
	out, err := json.Marshal(cm)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func p256JWK(k *ecdsa.PrivateKey, kid string) map[string]string {
	return map[string]string{
		"kty": "EC", "crv": "P-256", "kid": kid,
		"x": b64u(k.PublicKey.X.Bytes()), "y": b64u(k.PublicKey.Y.Bytes()),
	}
}

func baseCard() map[string]interface{} {
	return map[string]interface{}{
		"name":            "Weather Agent",
		"description":     "Provides weather",
		"protocolVersion": "0.3.0",
		"url":             "https://example.com",
		"version":         "1.2.3",
	}
}

func TestVerifyCardSignatureES256(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cardBytes := signCard(t, baseCard(), "key-1", "ES256", key, p256JWK(key, "key-1"), "")
	results, tampered := VerifyAgentCardSignature(cardBytes)
	if tampered {
		t.Fatalf("valid signature reported tampered: %+v", results)
	}
	if len(results) != 1 || results[0].Status != CardSigVerified {
		t.Fatalf("expected verified, got %+v (err=%v)", results, results[0].Err)
	}
	if results[0].Alg != "ES256" || results[0].KID != "key-1" {
		t.Errorf("alg/kid mismatch: %+v", results[0])
	}
}

func TestVerifyCardSignatureTampered(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cardBytes := signCard(t, baseCard(), "key-1", "ES256", key, p256JWK(key, "key-1"), "")
	// Tamper: bump the version after signing.
	var cm map[string]interface{}
	if err := json.Unmarshal(cardBytes, &cm); err != nil {
		t.Fatal(err)
	}
	cm["version"] = "9.9.9"
	tamperedBytes, _ := json.Marshal(cm)

	results, tampered := VerifyAgentCardSignature(tamperedBytes)
	if !tampered {
		t.Fatalf("tampered card not detected: %+v", results)
	}
	if results[0].Status != CardSigInvalid {
		t.Errorf("expected invalid status, got %s", results[0].Status)
	}
}

func TestVerifyCardSignatureRS256AndEdDSA(t *testing.T) {
	rkey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rjwk := map[string]string{
		"kty": "RSA", "kid": "rsa-1",
		"n": b64u(rkey.PublicKey.N.Bytes()), "e": b64u(big.NewInt(int64(rkey.PublicKey.E)).Bytes()),
	}
	cardBytes := signCard(t, baseCard(), "rsa-1", "RS256", rkey, rjwk, "")
	if results, tampered := VerifyAgentCardSignature(cardBytes); tampered || results[0].Status != CardSigVerified {
		t.Fatalf("RS256: tampered=%v results=%+v err=%v", tampered, results, results[0].Err)
	}

	pub, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ejwk := map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": "ed-1", "x": b64u(pub)}
	cardBytes = signCard(t, baseCard(), "ed-1", "EdDSA", edKey, ejwk, "")
	if results, tampered := VerifyAgentCardSignature(cardBytes); tampered || results[0].Status != CardSigVerified {
		t.Fatalf("EdDSA: tampered=%v results=%+v err=%v", tampered, results, results[0].Err)
	}
}

func TestVerifyCardSignatureUnsupportedAlg(t *testing.T) {
	// HS256 is symmetric and deliberately unsupported; it must NOT count as
	// tampering.
	cardBytes := signCard(t, baseCard(), "hmac-1", "HS256", nil,
		map[string]string{"kty": "oct", "kid": "hmac-1", "k": b64u([]byte("secret"))}, "")
	results, tampered := VerifyAgentCardSignature(cardBytes)
	if tampered {
		t.Fatalf("unsupported alg must not be tampering: %+v", results)
	}
	if results[0].Status != CardSigUnsupported {
		t.Errorf("expected unsupported-alg, got %s", results[0].Status)
	}
}

func TestVerifyCardSignatureJKULoopback(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	jwk := p256JWK(key, "jku-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": []map[string]string{jwk}})
	}))
	defer srv.Close()

	cardBytes := signCard(t, baseCard(), "jku-1", "ES256", key, nil, srv.URL+"/jwks.json")
	results, tampered := verifyAgentCardSignature(context.Background(), cardBytes, fetchJWKS)
	if tampered {
		t.Fatalf("jku verification failed: %+v err=%v", results, results[0].Err)
	}
	if results[0].Status != CardSigVerified {
		t.Errorf("expected verified via jku, got %s (err=%v)", results[0].Status, results[0].Err)
	}
}

func TestVerifyCardSignatureJKUPlainHTTPRejected(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cardBytes := signCard(t, baseCard(), "jku-1", "ES256", key, nil, "http://evil.example.com/jwks.json")
	results, tampered := verifyAgentCardSignature(context.Background(), cardBytes, fetchJWKS)
	if tampered {
		t.Fatal("unreachable jku must not be treated as tampering")
	}
	if results[0].Status != CardSigNoKey {
		t.Errorf("expected no-key, got %s", results[0].Status)
	}
}

func TestVerifyCardSignatureMissingFields(t *testing.T) {
	card := baseCard()
	card["signatures"] = []map[string]string{{"protected": "", "signature": ""}}
	cardBytes, _ := json.Marshal(card)
	results, tampered := VerifyAgentCardSignature(cardBytes)
	if !tampered {
		t.Fatal("empty signature block must be tampered (unverifiable structure)")
	}
	if results[0].Status != CardSigInvalid {
		t.Errorf("expected invalid, got %s", results[0].Status)
	}
}

func TestHasCardSignatures(t *testing.T) {
	if hasCardSignatures(json.RawMessage(`{"name":"x"}`)) {
		t.Error("no signatures field should be false")
	}
	if hasCardSignatures(json.RawMessage(`{"signatures":[]}`)) {
		t.Error("empty signatures array should be false")
	}
	if !hasCardSignatures(json.RawMessage(`{"signatures":[{"protected":"a"}]}`)) {
		t.Error("non-empty signatures should be true")
	}
}

// TestDiscoverTamperedCardRejected covers the end-to-end Discover policy: a
// card whose signature no longer matches is refused, a valid one is served.
func TestDiscoverTamperedCardRejected(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	validCard := signCard(t, baseCard(), "key-1", "ES256", key, p256JWK(key, "key-1"), "")
	var cm map[string]interface{}
	_ = json.Unmarshal(validCard, &cm)
	cm["name"] = "Evil Agent"
	tamperedCard, _ := json.Marshal(cm)

	mu := make(chan []byte, 1)
	mu <- validCard
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(<-mu)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	card, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("valid card should discover: %v", err)
	}
	if card.Name != "Weather Agent" {
		t.Errorf("unexpected card name %q", card.Name)
	}

	mu <- tamperedCard
	c.card.Store(nil) // force re-discovery
	_, err = c.Discover(ctx)
	if err == nil {
		t.Fatal("tampered card must be rejected by Discover")
	}
	if _, ok := err.(*CardTamperedError); !ok {
		t.Errorf("expected *CardTamperedError, got %T: %v", err, err)
	}
}

var _ = time.Second
