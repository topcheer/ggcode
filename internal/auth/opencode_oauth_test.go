package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenCodeDeviceAuthVerificationURL(t *testing.T) {
	d := &OpenCodeDeviceAuth{VerificationURIComplete: "/console/device?user_code=X&client_id=opencode-cli"}
	got := d.VerificationURL("https://opencode.ai/console")
	want := "https://opencode.ai/console/device?user_code=X&client_id=opencode-cli"
	if got != want {
		t.Fatalf("VerificationURL = %q, want %q", got, want)
	}
	// Absolute URLs pass through untouched.
	d2 := &OpenCodeDeviceAuth{VerificationURIComplete: "https://other.example/x"}
	if got := d2.VerificationURL("https://opencode.ai/console"); got != "https://other.example/x" {
		t.Fatalf("absolute passthrough = %q", got)
	}
}

func TestStartOpenCodeDeviceFlowMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/device/code" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") != "opencode/1.16.2" {
			t.Errorf("User-Agent = %q, want opencode/1.16.2", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"dc","user_code":"UC-1","verification_uri":"/console/device","verification_uri_complete":"/console/device?user_code=UC-1","expires_in":900,"interval":5}`))
	}))
	defer srv.Close()

	dev, err := StartOpenCodeDeviceFlow(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("StartOpenCodeDeviceFlow: %v", err)
	}
	if dev.DeviceCode != "dc" || dev.UserCode != "UC-1" {
		t.Fatalf("device auth = %+v", dev)
	}
	if u := dev.VerificationURL(srv.URL); u != srv.URL+"/console/device?user_code=UC-1" {
		t.Fatalf("VerificationURL = %q", u)
	}
}

func TestExchangeOpenCodeDeviceTokenPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"authorization_pending","error_description":"wait"}`))
	}))
	defer srv.Close()

	_, err := ExchangeOpenCodeDeviceToken(context.Background(), srv.URL, "dc")
	if err != ErrOpenCodePending {
		t.Fatalf("err = %v, want ErrOpenCodePending", err)
	}
}

func TestExchangeOpenCodeDeviceTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	tok, err := ExchangeOpenCodeDeviceToken(context.Background(), srv.URL, "dc")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" || tok.ExpiresIn != 3600 {
		t.Fatalf("token = %+v", tok)
	}
}

func TestRefreshOpenCodeTokenGrant(t *testing.T) {
	var gotGrant, gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			GrantType    string `json:"grant_type"`
			RefreshToken string `json:"refresh_token"`
			ClientID     string `json:"client_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotGrant, gotRefresh = body.GrantType, body.RefreshToken
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at2","refresh_token":"rt2","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	tok, err := RefreshOpenCodeToken(context.Background(), srv.URL, "rt-old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.AccessToken != "at2" {
		t.Fatalf("token = %+v", tok)
	}
	if gotGrant != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", gotGrant)
	}
	if gotRefresh != "rt-old" {
		t.Fatalf("refresh_token = %q, want rt-old", gotRefresh)
	}
}

func TestPollOpenCodeDeviceFlowPendingThenSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Write([]byte(`{"error":"authorization_pending","error_description":"wait"}`))
			return
		}
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	dev := &OpenCodeDeviceAuth{DeviceCode: "dc", UserCode: "UC", ExpiresIn: 60, Interval: 0} // interval<1s default
	info, err := PollOpenCodeDeviceFlow(context.Background(), srv.URL, dev)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if info.ProviderID != ProviderOpenCode || info.AccessToken != "at" || info.RefreshToken != "rt" {
		t.Fatalf("info = %+v", info)
	}
	if info.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt not set from expires_in")
	}
}

func TestPollOpenCodeDeviceFlowDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"access_denied","error_description":"no"}`))
	}))
	defer srv.Close()

	dev := &OpenCodeDeviceAuth{DeviceCode: "dc", UserCode: "UC", ExpiresIn: 60, Interval: 0}
	_, err := PollOpenCodeDeviceFlow(context.Background(), srv.URL, dev)
	if err != ErrOpenCodeDenied {
		t.Fatalf("err = %v, want ErrOpenCodeDenied", err)
	}
}

func TestPollOpenCodeDeviceFlowNilDev(t *testing.T) {
	if _, err := PollOpenCodeDeviceFlow(context.Background(), "", nil); err == nil {
		t.Fatal("expected error for nil device flow")
	}
}

func TestOpenCodeDeviceAuthVerificationURLEmptyConsole(t *testing.T) {
	// Unset OPENCODE_CONSOLE_URL: the resolved URL must still be absolute
	// (openers reject relative paths as non-http(s)) - the "browser never
	// opens" regression.
	d := &OpenCodeDeviceAuth{VerificationURIComplete: "/console/device?user_code=X"}
	got := d.VerificationURL("")
	if !strings.HasPrefix(got, "https://opencode.ai/") {
		t.Fatalf("VerificationURL(\"\") = %q, want absolute https://opencode.ai/... URL", got)
	}
}
