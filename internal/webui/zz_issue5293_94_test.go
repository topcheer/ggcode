package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #5293: endpoint PUT was a full-struct replace - fields absent from the
// client's JSON (api_key + ~20 others not in the GET projection) were
// zeroed on every save. #5294: vendor PUT decoded api_key as a plain
// string, so an absent field ("" != "__unchanged__") destroyed the
// stored key. Both fixed: vendor PUT uses pointer semantics (nil =
// keep), endpoint PUT merges by key presence.

func TestIssue5294_VendorPUTAbsentAPIKeyKeepsStoredKey(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	// set a real key first; SetVendorAPIKey stores an env-reference form,
	// so capture whatever it persisted via the stored field itself
	if err := s.cfg.SetVendorAPIKey("testvendor", "sk-secret"); err != nil {
		t.Fatal(err)
	}
	stored := s.cfg.Vendors["testvendor"].APIKey
	if stored == "" {
		t.Fatal("SetVendorAPIKey produced empty stored field")
	}

	// PUT with api_key ABSENT (display_name only) - must NOT clear the key
	w := httptest.NewRecorder()
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor", `{"display_name":"Renamed"}`)
	req.SetPathValue("vendor", "testvendor")
	s.handleVendorDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rename PUT failed: %d %s", w.Code, w.Body.String())
	}
	if got := s.cfg.Vendors["testvendor"].APIKey; got != stored {
		t.Errorf("absent api_key destroyed stored key: got %q", got)
	}
	if s.cfg.Vendors["testvendor"].DisplayName != "Renamed" {
		t.Errorf("display_name not applied: %q", s.cfg.Vendors["testvendor"].DisplayName)
	}
}

func TestIssue5294_VendorPUTExplicitEmptyClearsKey(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	if err := s.cfg.SetVendorAPIKey("testvendor", "sk-secret"); err != nil {
		t.Fatal(err)
	}
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor", `{"api_key":""}`)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("explicit clear failed: %d %s", w.Code, w.Body.String())
	}
	if got := s.cfg.Vendors["testvendor"].APIKey; got != "" {
		t.Errorf("explicit empty string should clear key, got %q", got)
	}
}

func TestIssue5293_EndpointPUTPreservesUnprojectedFields(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	// seed the stored endpoint with fields a partial GET projection may omit
	s.mu.Lock()
	ep := s.cfg.Vendors["testvendor"].Endpoints["prod"]
	ep.APIKey = "sk-endpoint-secret"
	ep.MaxTokens = 8192
	ep.Models = []string{"gpt-x", "gpt-y"}
	ep.DefaultModel = "gpt-x"
	s.cfg.Vendors["testvendor"].Endpoints["prod"] = ep
	s.mu.Unlock()

	// client saves only base_url (partial projection)
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/prod", `{"base_url":"https://new.test.com"}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("endpoint PUT failed: %d %s", w.Code, w.Body.String())
	}

	got := s.cfg.Vendors["testvendor"].Endpoints["prod"]
	if got.BaseURL != "https://new.test.com" {
		t.Errorf("base_url not updated: %q", got.BaseURL)
	}
	if got.APIKey != "sk-endpoint-secret" {
		t.Errorf("unprojected api_key destroyed: %q", got.APIKey)
	}
	if got.MaxTokens != 8192 {
		t.Errorf("unprojected max_tokens destroyed: %d", got.MaxTokens)
	}
	if len(got.Models) != 2 || got.DefaultModel != "gpt-x" {
		t.Errorf("unprojected models/default_model destroyed: %v %q", got.Models, got.DefaultModel)
	}
}

func TestIssue5293_EndpointPUTExplicitClearStillWorks(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	s.mu.Lock()
	ep := s.cfg.Vendors["testvendor"].Endpoints["prod"]
	ep.APIKey = "sk-endpoint-secret"
	s.cfg.Vendors["testvendor"].Endpoints["prod"] = ep
	s.mu.Unlock()

	// explicit api_key:"" must clear (client intends removal)
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/prod", `{"api_key":""}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("explicit clear failed: %d %s", w.Code, w.Body.String())
	}
	if got := s.cfg.Vendors["testvendor"].Endpoints["prod"].APIKey; got != "" {
		t.Errorf("explicit empty should clear endpoint api_key, got %q", got)
	}
}

func TestIssue5293_EndpointPUTNewEndpointPartialBody(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	// creating a new endpoint with a partial body must not require all fields
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/newep", `{"protocol":"openai","base_url":"https://n.test.com"}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "newep")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("new endpoint PUT failed: %d %s", w.Code, w.Body.String())
	}
	got := s.cfg.Vendors["testvendor"].Endpoints["newep"]
	if got.Protocol != "openai" || got.BaseURL != "https://n.test.com" {
		t.Errorf("new endpoint fields wrong: %+v", got)
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
}
