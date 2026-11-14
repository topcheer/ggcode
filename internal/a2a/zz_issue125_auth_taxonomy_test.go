package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #125/G2: auth reason taxonomy - first failing arm is classified, and a
// presented-but-invalid credential is rejected outright (no silent
// fall-through to another arm).
func TestIssue125_AuthReasonTaxonomy(t *testing.T) {
	cases := []struct {
		name   string
		srv    *Server
		header func(*http.Request)
		wantOK bool
		want   string
	}{
		{"api_key_valid", &Server{apiKeys: []string{"k1"}}, func(r *http.Request) {
			r.Header.Set("X-API-Key", "k1")
		}, true, ""},
		{"api_key_invalid", &Server{apiKeys: []string{"k1"}}, func(r *http.Request) {
			r.Header.Set("X-API-Key", "wrong")
		}, false, "api_key_invalid"},
		{"api_key_missing", &Server{apiKeys: []string{"k1"}}, func(r *http.Request) {}, false, "api_key_missing"},
		{"bearer_validator_absent", &Server{}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer tok")
		}, false, "bearer_validator_absent"},
		{"mtls_missing", &Server{mtlsEnabled: true}, func(r *http.Request) {}, false, "mtls_client_cert_missing"},
		{"open_arm_allowed", &Server{}, func(r *http.Request) {}, true, ""},
		{"open_arm_refused", &Server{requireAuth: true}, func(r *http.Request) {}, false, "unauthenticated_denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			tc.header(r)
			ok, reason := tc.srv.authenticateRequest(r)
			if ok != tc.wantOK || reason != tc.want {
				t.Fatalf("got ok=%v reason=%q, want ok=%v reason=%q", ok, reason, tc.wantOK, tc.want)
			}
		})
	}
}

// #125/G1: 401 carries WWW-Authenticate + X-Auth-Reason; body stays
// "unauthorized" for wire compatibility.
func TestIssue125_ChallengeHeadersOn401(t *testing.T) {
	s := &Server{apiKeys: []string{"k1"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRPC)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ch := resp.Header.Get("WWW-Authenticate"); !strings.Contains(ch, "X-API-Key") {
		t.Fatalf("challenge = %q", ch)
	}
	if reason := resp.Header.Get("X-Auth-Reason"); reason != "api_key_missing" {
		t.Fatalf("reason = %q", reason)
	}
	buf := make([]byte, 128)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "unauthorized") {
		t.Fatalf("body = %q", string(buf[:n]))
	}
}

// #125/G4: the CLI surfaces an actionable 401 diagnosis instead of a
// JSON decode error.
func TestIssue125_CLI401Diagnosis(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-Reason", "api_key_invalid")
		w.Header().Set("WWW-Authenticate", `X-API-Key realm="ggcode-a2a"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()

	_, err := IntrospectPeerV3(context.Background(), ts.URL, "bad", SnapshotRequest{Depth: DepthFull})
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	for _, want := range []string{"HTTP 401", "unauthorized", "reason=api_key_invalid", "remediation", "X-API-Key"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error missing %q: %s", want, msg)
		}
	}
}
