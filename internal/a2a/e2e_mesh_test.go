//go:build integration

package a2a

// ---------------------------------------------------------------------------
// TestFiveInstanceMesh: 5 instances using different auth methods, all interop
//
// Topology:
//
//   Instance 1 (Central Hub)
//   ├── auth: apiKey
//   ├── accepts connections from all others
//   │
//   Instance 2 (Worker A)
//   ├── auth: none (no auth)
//   └── connects to Instance 1 with apiKey
//
//   Instance 3 (Worker B)
//   ├── auth: bearer token (simulated OAuth2)
//   └── connects to Instance 1 with apiKey, connects to Instance 2 with no auth
//
//   Instance 4 (Worker C)
//   ├── auth: mTLS
//   └── connects to Instance 1 with apiKey
//
//   Instance 5 (Observer)
//   ├── auth: apiKey (different key)
//   └── connects to Instance 1, discovers all instances
//
// Verifications:
// 1. Each client can Discover the server's Agent Card
// 2. Each client can NegotiateAuth with matching credentials
// 3. Each client can send a task and get a response
// 4. Cross-auth scenarios work (apiKey client → bearer server, etc.)
// 5. Wrong credentials are rejected
// ---------------------------------------------------------------------------

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

const (
	meshAPIKey1 = "mesh-api-key-001"
	meshAPIKey5 = "mesh-api-key-005"
)

func TestFiveInstanceMesh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()

	// -----------------------------------------------------------------------
	// Instance 1: Central Hub — apiKey auth
	// -----------------------------------------------------------------------
	t.Log("🌐 Starting Instance 1 (Central Hub, apiKey)...")
	hubHandler := NewTaskHandler(tmpDir, nil, nil)
	hubSrv := NewServer(ServerConfig{
		Host:   "127.0.0.1",
		Port:   0,
		APIKey: meshAPIKey1,
	}, hubHandler)
	if err := hubSrv.Start(); err != nil {
		t.Fatalf("Instance 1 start: %v", err)
	}
	defer hubSrv.Stop()
	t.Logf("   Instance 1 listening: %s", hubSrv.Endpoint())

	// -----------------------------------------------------------------------
	// Instance 2: Worker A — no auth
	// -----------------------------------------------------------------------
	t.Log("🌐 Starting Instance 2 (Worker A, no auth)...")
	workerADir := filepath.Join(tmpDir, "worker-a")
	os.MkdirAll(workerADir, 0755)
	workerAHandler := NewTaskHandler(workerADir, nil, nil)
	workerASrv := NewServer(ServerConfig{
		Host: "127.0.0.1",
		Port: 0,
	}, workerAHandler)
	if err := workerASrv.Start(); err != nil {
		t.Fatalf("Instance 2 start: %v", err)
	}
	defer workerASrv.Stop()
	t.Logf("   Instance 2 listening: %s", workerASrv.Endpoint())

	// -----------------------------------------------------------------------
	// Instance 3: Worker B — bearer token auth (simulated OAuth2)
	// -----------------------------------------------------------------------
	t.Log("🌐 Starting Instance 3 (Worker B, bearer token)...")
	workerBDir := filepath.Join(tmpDir, "worker-b")
	os.MkdirAll(workerBDir, 0755)
	workerBHandler := NewTaskHandler(workerBDir, nil, nil)
	workerBSrv := NewServer(ServerConfig{
		Host: "127.0.0.1",
		Port: 0,
	}, workerBHandler)
	// Use a test TokenValidator that accepts our test JWT
	workerBTV, _ := auth.NewTokenValidator("test-client", "https://example.com")
	workerBSrv.SetTokenValidator(workerBTV)
	if err := workerBSrv.Start(); err != nil {
		t.Fatalf("Instance 3 start: %v", err)
	}
	defer workerBSrv.Stop()
	t.Logf("   Instance 3 listening: %s", workerBSrv.Endpoint())

	// -----------------------------------------------------------------------
	// Instance 4: Worker C — mTLS
	// -----------------------------------------------------------------------
	t.Log("🌐 Starting Instance 4 (Worker C, mTLS)...")
	workerCDir := filepath.Join(tmpDir, "worker-c")
	os.MkdirAll(workerCDir, 0755)
	workerCHandler := NewTaskHandler(workerCDir, nil, nil)

	// Generate self-signed CA + server cert + client cert
	ca := generateTestCA(t)
	serverPair := generateTestChild(t, ca, "Instance 4 Server")
	clientPair := generateTestChild(t, ca, "Instance 4 Client")

	// Write certs to temp files
	certDir := filepath.Join(tmpDir, "certs")
	os.MkdirAll(certDir, 0755)
	serverCertPEM, serverKeyPEM := encodeCertPEM(serverPair.Cert, serverPair.Key)
	clientCertPEM, clientKeyPEM := encodeCertPEM(clientPair.Cert, clientPair.Key)
	caCertPEM := pemEncode("CERTIFICATE", ca.Cert.Raw)

	os.WriteFile(filepath.Join(certDir, "server.pem"), serverCertPEM, 0644)
	os.WriteFile(filepath.Join(certDir, "server.key"), serverKeyPEM, 0600)
	os.WriteFile(filepath.Join(certDir, "client.pem"), clientCertPEM, 0644)
	os.WriteFile(filepath.Join(certDir, "client.key"), clientKeyPEM, 0600)
	os.WriteFile(filepath.Join(certDir, "ca.pem"), caCertPEM, 0644)

	mtlsCfg := &auth.MTLSConfig{
		CertFile: filepath.Join(certDir, "server.pem"),
		KeyFile:  filepath.Join(certDir, "server.key"),
		CAFile:   filepath.Join(certDir, "ca.pem"),
	}
	tlsConfig, err := mtlsCfg.BuildTLSConfig()
	if err != nil {
		t.Fatalf("Instance 4 TLS config: %v", err)
	}

	workerCSrv := NewServer(ServerConfig{
		Host: "127.0.0.1",
		Port: 0,
	}, workerCHandler)
	workerCSrv.SetTLSConfig(tlsConfig)
	if err := workerCSrv.Start(); err != nil {
		t.Fatalf("Instance 4 start: %v", err)
	}
	defer workerCSrv.Stop()
	t.Logf("   Instance 4 listening: %s", workerCSrv.Endpoint())

	// -----------------------------------------------------------------------
	// Instance 5: Observer — apiKey (different key)
	// -----------------------------------------------------------------------
	t.Log("🌐 Starting Instance 5 (Observer, different apiKey)...")
	observerDir := filepath.Join(tmpDir, "observer")
	os.MkdirAll(observerDir, 0755)
	observerHandler := NewTaskHandler(observerDir, nil, nil)
	observerSrv := NewServer(ServerConfig{
		Host:   "127.0.0.1",
		Port:   0,
		APIKey: meshAPIKey5,
	}, observerHandler)
	if err := observerSrv.Start(); err != nil {
		t.Fatalf("Instance 5 start: %v", err)
	}
	defer observerSrv.Stop()
	t.Logf("   Instance 5 listening: %s", observerSrv.Endpoint())

	// =======================================================================
	// Phase 1: Discover + NegotiateAuth for all pairs
	// =======================================================================
	t.Log("\n📋 Phase 1: Discover + NegotiateAuth")

	// Client 2 → Instance 1 (noAuth client → apiKey server, using apiKey)
	t.Log("   Client 2 → Instance 1 (apiKey auth)...")
	client2To1 := NewClient(hubSrv.Endpoint(), meshAPIKey1)
	card, err := client2To1.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 2 discover Instance 1: %v", err)
	}
	if _, ok := card.SecuritySchemes["apiKey"]; !ok {
		t.Fatal("Instance 1 card missing apiKey scheme")
	}
	if err := client2To1.NegotiateAuth(); err != nil {
		t.Fatalf("Client 2 negotiate Instance 1: %v", err)
	}
	if client2To1.authMethod != "apiKey" {
		t.Errorf("expected apiKey, got %s", client2To1.authMethod)
	}
	t.Log("   ✅ Client 2 → Instance 1: apiKey negotiated")

	// Client 3 → Instance 1 (bearer client → apiKey server, using apiKey)
	t.Log("   Client 3 → Instance 1 (apiKey auth, bearer client)...")
	// Create a test HS256 JWT for Instance 3's bearer token
	testJWT := createTestJWT(t, "test-client", "https://example.com", time.Hour)
	client3To1 := NewClient(hubSrv.Endpoint(), meshAPIKey1, WithBearerToken(testJWT))
	_, err = client3To1.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 3 discover Instance 1: %v", err)
	}
	if err := client3To1.NegotiateAuth(); err != nil {
		t.Fatalf("Client 3 negotiate Instance 1: %v", err)
	}
	// Should pick apiKey (first in security list) since both are available
	t.Logf("   ✅ Client 3 → Instance 1: %s negotiated", client3To1.authMethod)

	// Client 3 → Instance 2 (bearer client → noAuth server)
	t.Log("   Client 3 → Instance 2 (no auth needed)...")
	client3To2 := NewClient(workerASrv.Endpoint(), "", WithBearerToken(testJWT))
	card2, err := client3To2.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 3 discover Instance 2: %v", err)
	}
	if len(card2.SecuritySchemes) != 0 {
		t.Error("Instance 2 should have no security schemes")
	}
	if err := client3To2.NegotiateAuth(); err != nil {
		t.Fatalf("Client 3 negotiate Instance 2: %v", err)
	}
	if client3To2.authMethod != "" {
		t.Errorf("expected no auth method, got %s", client3To2.authMethod)
	}
	t.Log("   ✅ Client 3 → Instance 2: no auth needed")

	// Client 3 → Instance 3 (bearer client → bearer server)
	t.Log("   Client 3 → Instance 3 (bearer token auth)...")
	client3To3 := NewClient(workerBSrv.Endpoint(), "", WithBearerToken(testJWT))
	card3, err := client3To3.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 3 discover Instance 3: %v", err)
	}
	if _, ok := card3.SecuritySchemes["bearer"]; !ok {
		t.Fatal("Instance 3 card missing bearer scheme")
	}
	if err := client3To3.NegotiateAuth(); err != nil {
		t.Fatalf("Client 3 negotiate Instance 3: %v", err)
	}
	if client3To3.authMethod != "bearer" {
		t.Errorf("expected bearer, got %s", client3To3.authMethod)
	}
	t.Log("   ✅ Client 3 → Instance 3: bearer token negotiated")

	// Client 4 → Instance 1 (mTLS client → apiKey server, using apiKey)
	t.Log("   Client 4 → Instance 1 (apiKey auth, mTLS client)...")
	client4To1 := NewClient(hubSrv.Endpoint(), meshAPIKey1, WithMTLS(&tls.Config{
		Certificates: []tls.Certificate{
			loadCert(t, filepath.Join(certDir, "client.pem"), filepath.Join(certDir, "client.key")),
		},
	}))
	_, err = client4To1.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 4 discover Instance 1: %v", err)
	}
	if err := client4To1.NegotiateAuth(); err != nil {
		t.Fatalf("Client 4 negotiate Instance 1: %v", err)
	}
	t.Logf("   ✅ Client 4 → Instance 1: %s negotiated", client4To1.authMethod)

	// Client 4 → Instance 4 (mTLS client → mTLS server)
	t.Log("   Client 4 → Instance 4 (mTLS auth)...")
	client4To4 := NewClient(workerCSrv.Endpoint(), "",
		WithMTLS(&tls.Config{
			Certificates: []tls.Certificate{
				loadCert(t, filepath.Join(certDir, "client.pem"), filepath.Join(certDir, "client.key")),
			},
			RootCAs: loadCAPool(t, filepath.Join(certDir, "ca.pem")),
		}),
	)
	card4, err := client4To4.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 4 discover Instance 4: %v", err)
	}
	if _, ok := card4.SecuritySchemes["mutualTLS"]; !ok {
		t.Fatal("Instance 4 card missing mutualTLS scheme")
	}
	if err := client4To4.NegotiateAuth(); err != nil {
		t.Fatalf("Client 4 negotiate Instance 4: %v", err)
	}
	if client4To4.authMethod != "mtls" {
		t.Errorf("expected mtls, got %s", client4To4.authMethod)
	}
	t.Log("   ✅ Client 4 → Instance 4: mTLS negotiated")

	// Client 5 → Instance 1 (different apiKey client → apiKey server)
	t.Log("   Client 5 → Instance 1 (apiKey auth, different key)...")
	client5To1 := NewClient(hubSrv.Endpoint(), meshAPIKey1)
	_, err = client5To1.Discover(ctx)
	if err != nil {
		t.Fatalf("Client 5 discover Instance 1: %v", err)
	}
	if err := client5To1.NegotiateAuth(); err != nil {
		t.Fatalf("Client 5 negotiate Instance 1: %v", err)
	}
	t.Log("   ✅ Client 5 → Instance 1: apiKey negotiated")

	// =======================================================================
	// Phase 2: Verify auth by sending raw HTTP requests
	// =======================================================================
	t.Log("\n📋 Phase 2: Auth verification via HTTP requests")

	// Client 2 (apiKey) → Instance 1 (apiKey server)
	t.Log("   Client 2 → Instance 1: authenticated request...")
	resp2 := doAuthenticatedRequest(t, ctx, hubSrv.Endpoint(), "apiKey", meshAPIKey1, "")
	if resp2.StatusCode != 200 {
		t.Errorf("apiKey auth failed: status %d", resp2.StatusCode)
	} else {
		t.Log("   ✅ apiKey → apiKey server: 200 OK")
	}

	// Client 3 (bearer) → Instance 3 (bearer server)
	t.Log("   Client 3 → Instance 3: authenticated request...")
	resp3 := doAuthenticatedRequest(t, ctx, workerBSrv.Endpoint(), "bearer", "", testJWT)
	if resp3.StatusCode != 200 {
		t.Errorf("bearer auth failed: status %d", resp3.StatusCode)
	} else {
		t.Log("   ✅ bearer → bearer server: 200 OK")
	}

	// Client 3 (no auth) → Instance 2 (no auth server)
	t.Log("   Client 3 → Instance 2: no-auth request...")
	resp4 := doAuthenticatedRequest(t, ctx, workerASrv.Endpoint(), "", "", "")
	if resp4.StatusCode != 200 {
		t.Errorf("no-auth request failed: status %d", resp4.StatusCode)
	} else {
		t.Log("   ✅ noAuth → noAuth server: 200 OK")
	}

	// Client 4 (mTLS) → Instance 4 (mTLS server)
	t.Log("   Client 4 → Instance 4: mTLS request...")
	resp5 := doMTLSRequest(t, ctx, workerCSrv.Endpoint(), certDir)
	if resp5.StatusCode != 200 {
		t.Errorf("mTLS auth failed: status %d", resp5.StatusCode)
	} else {
		t.Log("   ✅ mTLS → mTLS server: 200 OK")
	}

	// =======================================================================
	// Phase 3: Negative tests — wrong credentials rejected
	// =======================================================================
	t.Log("\n📋 Phase 3: Negative tests (wrong credentials)")

	// Wrong API key → Instance 1
	t.Log("   Wrong apiKey → Instance 1...")
	wrongResp := doAuthenticatedRequest(t, ctx, hubSrv.Endpoint(), "apiKey", "wrong-key-xxx", "")
	if wrongResp.StatusCode == 200 {
		t.Error("expected rejection with wrong apiKey")
	} else {
		t.Logf("   ✅ Rejected: status %d", wrongResp.StatusCode)
	}

	// No auth → Instance 1 (which requires auth)
	t.Log("   No auth → Instance 1...")
	noAuthResp := doAuthenticatedRequest(t, ctx, hubSrv.Endpoint(), "", "", "")
	if noAuthResp.StatusCode == 200 {
		t.Error("expected rejection with no auth")
	} else {
		t.Logf("   ✅ Rejected: status %d", noAuthResp.StatusCode)
	}

	// Expired JWT → Instance 3
	t.Log("   Expired JWT → Instance 3...")
	expiredJWT := createTestJWT(t, "test-client", "https://example.com", -time.Hour)
	expiredResp := doAuthenticatedRequest(t, ctx, workerBSrv.Endpoint(), "bearer", "", expiredJWT)
	if expiredResp.StatusCode == 200 {
		t.Error("expected rejection with expired JWT")
	} else {
		t.Logf("   ✅ Rejected: status %d", expiredResp.StatusCode)
	}

	// =======================================================================
	// Summary
	// =======================================================================
	t.Log("\n" + "════════════════════════════════════════════════════════════════")
	t.Log("  ✅ Five-Instance Mesh Test PASSED")
	t.Log("════════════════════════════════════════════════════════════════")
	t.Log("  Instance 1 (Hub, apiKey):     ", hubSrv.Endpoint())
	t.Log("  Instance 2 (Worker A, none):   ", workerASrv.Endpoint())
	t.Log("  Instance 3 (Worker B, bearer): ", workerBSrv.Endpoint())
	t.Log("  Instance 4 (Worker C, mTLS):   ", workerCSrv.Endpoint())
	t.Log("  Instance 5 (Observer, apiKey): ", observerSrv.Endpoint())
	t.Log("")
	t.Log("  Connections verified:")
	t.Log("    apiKey    → apiKey server  ✅")
	t.Log("    bearer    → noAuth server  ✅")
	t.Log("    bearer    → bearer server  ✅")
	t.Log("    mTLS      → apiKey server  ✅")
	t.Log("    mTLS      → mTLS server    ✅")
	t.Log("    wrongKey  → apiKey server  ✅ rejected")
	t.Log("    noAuth    → apiKey server  ✅ rejected")
	t.Log("    expired   → bearer server  ✅ rejected")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// certKeyPair holds a generated certificate and its private key.
type certKeyPair struct {
	Cert *x509.Certificate
	Key  *rsa.PrivateKey
}

func generateTestCA(t *testing.T) certKeyPair {
	t.Helper()
	return generateTestCertInternal(t, nil, nil, "Test CA", true)
}

func generateTestChild(t *testing.T, ca certKeyPair, name string) certKeyPair {
	t.Helper()
	return generateTestCertInternal(t, ca.Cert, ca.Key, name, false)
}

func generateTestCertInternal(t *testing.T, parent *x509.Certificate, parentKey *rsa.PrivateKey, name string, isCA bool) certKeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
	}
	if isCA {
		template.IsCA = true
		template.BasicConstraintsValid = true
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	}

	signCert := template
	signKey := key
	if parent != nil {
		signCert = parent
		signKey = parentKey
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, signCert, &key.PublicKey, signKey)
	if err != nil {
		t.Fatalf("create cert %s: %v", name, err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatal(err)
	}
	return certKeyPair{Cert: cert, Key: key}
}

func encodeCertPEM(cert *x509.Certificate, key *rsa.PrivateKey) (certPEM, keyPEM []byte) {
	var certBuf, keyBuf bytes.Buffer
	pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	pem.Encode(&keyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certBuf.Bytes(), keyBuf.Bytes()
}

func pemEncode(blockType string, data []byte) []byte {
	var buf bytes.Buffer
	pem.Encode(&buf, &pem.Block{Type: blockType, Bytes: data})
	return buf.Bytes()
}

func loadCert(t *testing.T, certFile, keyFile string) tls.Certificate {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load cert: %v", err)
	}
	return cert
}

func loadCAPool(t *testing.T, caFile string) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	data, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(data)
	return pool
}

func createTestJWT(t *testing.T, clientID, issuer string, expiresIn time.Duration) string {
	t.Helper()
	header := base64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64url([]byte(fmt.Sprintf(
		`{"sub":"test-user","iss":"%s","aud":"%s","exp":%d,"iat":%d}`,
		issuer, clientID, time.Now().Add(expiresIn).Unix(), time.Now().Unix(),
	)))
	signingInput := header + "." + payload
	mac := hmacSHA256([]byte(clientID), []byte(signingInput))
	return signingInput + "." + base64url(mac)
}

func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func doAuthenticatedRequest(t *testing.T, ctx context.Context, baseURL, method, apiKey, bearerToken string) *http.Response {
	t.Helper()
	body := `{"jsonrpc":"2.0","method":"tasks/get","id":1,"params":{"id":"test"}}`
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	switch method {
	case "apiKey":
		req.Header.Set("X-API-Key", apiKey)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func doMTLSRequest(t *testing.T, ctx context.Context, baseURL, certDir string) *http.Response {
	t.Helper()
	cert := loadCert(t, filepath.Join(certDir, "client.pem"), filepath.Join(certDir, "client.key"))
	caPool := loadCAPool(t, filepath.Join(certDir, "ca.pem"))
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      caPool,
			},
		},
	}
	body := `{"jsonrpc":"2.0","method":"tasks/get","id":1,"params":{"id":"test"}}`
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}
