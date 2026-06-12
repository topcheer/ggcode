package util

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestInsecureMode_Default(t *testing.T) {
	// Reset cached value for test
	insecureOnce = sync.Once{}
	insecureValue = false

	if InsecureMode() {
		t.Error("InsecureMode() should be false by default")
	}
}

func TestInsecureMode_Enabled(t *testing.T) {
	// Reset cached value for test
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "1")
	defer os.Unsetenv("GGCODE_INSECURE")

	if !InsecureMode() {
		t.Error("InsecureMode() should be true when GGCODE_INSECURE=1")
	}
}

func TestInsecureMode_True(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "true")
	defer os.Unsetenv("GGCODE_INSECURE")

	if !InsecureMode() {
		t.Error("InsecureMode() should be true when GGCODE_INSECURE=true")
	}
}

func TestInsecureMode_Yes(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "yes")
	defer os.Unsetenv("GGCODE_INSECURE")

	if !InsecureMode() {
		t.Error("InsecureMode() should be true when GGCODE_INSECURE=yes")
	}
}

func TestInsecureMode_Disabled(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "false")
	defer os.Unsetenv("GGCODE_INSECURE")

	if InsecureMode() {
		t.Error("InsecureMode() should be false when GGCODE_INSECURE=false")
	}
}

func TestWrapTransport_InsecureOff(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false
	os.Unsetenv("GGCODE_INSECURE")

	base := &http.Transport{}
	result := WrapTransport(base)

	// Should be a clone, not the same pointer
	if result == base {
		t.Error("WrapTransport should return a clone")
	}
	// Should NOT have InsecureSkipVerify
	if result.TLSClientConfig != nil && result.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false when GGCODE_INSECURE is not set")
	}
}

func TestWrapTransport_InsecureOn(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "1")
	defer os.Unsetenv("GGCODE_INSECURE")

	base := &http.Transport{}
	result := WrapTransport(base)

	if result.TLSClientConfig == nil || !result.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when GGCODE_INSECURE=1")
	}
}

func TestWrapTransport_NilBase(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "1")
	defer os.Unsetenv("GGCODE_INSECURE")

	result := WrapTransport(nil)

	if result == nil {
		t.Fatal("WrapTransport(nil) should return non-nil transport")
	}
	if result.TLSClientConfig == nil || !result.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when GGCODE_INSECURE=1")
	}
}

func TestNewInsecureAwareClient(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "1")
	defer os.Unsetenv("GGCODE_INSECURE")

	client := NewInsecureAwareClient(10 * time.Second)
	if client == nil {
		t.Fatal("NewInsecureAwareClient should return non-nil client")
	}
	if client.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", client.Timeout)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport should be *http.Transport")
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

func TestInsecureAwareClient_SelfSignedCert(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "1")
	defer os.Unsetenv("GGCODE_INSECURE")

	// Create a self-signed test server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewInsecureAwareClient(5 * time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Request to self-signed server should succeed with GGCODE_INSECURE=1, got: %v", err)
	}
	resp.Body.Close()
}

func TestInsecureAwareClient_SelfSignedCert_Rejected(t *testing.T) {
	insecureOnce = sync.Once{}
	insecureValue = false
	os.Unsetenv("GGCODE_INSECURE")

	// Create a self-signed test server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewInsecureAwareClient(5 * time.Second)
	_, err := client.Get(server.URL)
	if err == nil {
		t.Error("Request to self-signed server should fail without GGCODE_INSECURE")
	}
}

func TestWrapTransport_PreservesExistingTLSConfig(t *testing.T) {
	// This package caches insecure mode in globals; these tests must stay
	// non-parallel unless the implementation becomes injectable.
	insecureOnce = sync.Once{}
	insecureValue = false

	os.Setenv("GGCODE_INSECURE", "1")
	defer os.Unsetenv("GGCODE_INSECURE")

	existing := &tls.Config{MinVersion: tls.VersionTLS12}
	base := &http.Transport{TLSClientConfig: existing}

	result := WrapTransport(base)
	if result.TLSClientConfig == nil {
		t.Fatal("WrapTransport should preserve a TLSClientConfig")
	}
	if result.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Error("WrapTransport should preserve existing TLSClientConfig values")
	}
	if !result.TLSClientConfig.InsecureSkipVerify {
		t.Error("Should set InsecureSkipVerify on existing config")
	}
}
