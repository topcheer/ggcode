package tunnel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestExtractURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"da8ef0ad50eb4d.lhr.life tunneled with tls termination, https://da8ef0ad50eb4d.lhr.life",
			"https://da8ef0ad50eb4d.lhr.life",
		},
		{
			"abc123.lhr.life tunneled with tls termination, https://abc123.lhr.life\ncreate an account",
			"https://abc123.lhr.life",
		},
		{"no url here", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractURL(tt.input)
		if got != tt.want {
			t.Errorf("extractURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseJSONEvent(t *testing.T) {
	line := `{"connection_id":"abc","event":"tcpip-forward","message":"test.lhr.life tunneled with tls termination, https://test.lhr.life"}`
	var evt Event
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Event != "tcpip-forward" {
		t.Errorf("event = %q, want tcpip-forward", evt.Event)
	}
	url := extractURL(evt.Message)
	if url != "https://test.lhr.life" {
		t.Errorf("url = %q, want https://test.lhr.life", url)
	}
}

func TestTunnelNoListener(t *testing.T) {
	// Test that Start fails gracefully when no service is listening
	tun := New(19999) // port nobody listens on
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tun.Start(ctx)
	if err == nil {
		t.Fatal("expected error when no service is listening")
	}
	t.Logf("error (expected): %v", err)
}

func TestTunnelWithLocalServer(t *testing.T) {
	// Start a dummy HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from tunnel test"))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	server := &http.Server{Handler: mux}
	go server.Serve(ln)
	defer server.Close()

	tun := New(port)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url, err := tun.Start(ctx)
	if err != nil {
		t.Skipf("localhost.run tunnel failed (network issue?): %v", err)
		return
	}
	defer tun.Stop()

	if url == "" {
		t.Fatal("got empty URL")
	}
	t.Logf("Tunnel URL: %s", url)

	if tun.URL() != url {
		t.Errorf("URL() = %q, want %q", tun.URL(), url)
	}

	// Verify the tunnel works
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
