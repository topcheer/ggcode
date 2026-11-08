package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// zz_issue5293_test.go: PUT /api/vendors/{v}/endpoints/{e} must merge onto the
// existing config instead of replacing it wholesale. The GET projection
// (sanitizeEndpoints) only exposes base_url/protocol/models, so a round-tripped
// PUT carries just those fields; the previous wholesale replacement silently
// wiped APIKey (incl. ${ENV} refs), max_tokens,
// context_window, selected_model and ~17 other unprojected fields.

func TestIssue5293_EndpointPutPreservesUnprojectedFields(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	seed := config.EndpointConfig{
		Protocol:      "openai",
		BaseURL:       "https://api.test.com",
		APIKey:        "${TEST_5293_ENDPOINT_KEY}",
		MaxTokens:     4096,
		ContextWindow: 200000,
		SelectedModel: "glm-4.5",
		Models:        []string{"glm-4.5", "glm-4.5-air"},
	}
	s.cfg.Vendors["testvendor"].Endpoints["prod"] = seed

	// Round-tripped body from VendorsPage saveEndpoint: only projected fields.
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/prod",
		`{"base_url":"https://new.example.com","protocol":"openai","has_api_key":true}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := s.cfg.Vendors["testvendor"].Endpoints["prod"]
	if got.BaseURL != "https://new.example.com" {
		t.Errorf("base_url should be updated, got %q", got.BaseURL)
	}
	if got.APIKey != "${TEST_5293_ENDPOINT_KEY}" {
		t.Errorf("APIKey ref must be preserved, got %q", got.APIKey)
	}
	if got.MaxTokens != 4096 {
		t.Errorf("MaxTokens must be preserved, got %d", got.MaxTokens)
	}
	if got.ContextWindow != 200000 {
		t.Errorf("ContextWindow must be preserved, got %d", got.ContextWindow)
	}
	if got.SelectedModel != "glm-4.5" {
		t.Errorf("SelectedModel must be preserved, got %q", got.SelectedModel)
	}
	if len(got.Models) != 2 {
		t.Errorf("Models must be preserved when PUT omits them, got %v", got.Models)
	}
}

func TestIssue5293_EndpointPutUpdatesModelsWhenProvided(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	seed := config.EndpointConfig{
		Protocol: "openai",
		BaseURL:  "https://api.test.com",
		APIKey:   "sk-keep",
		Models:   []string{"old-model"},
	}
	s.cfg.Vendors["testvendor"].Endpoints["prod"] = seed

	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/prod",
		`{"base_url":"https://api.test.com","protocol":"openai","models":["new-model-a","new-model-b"]}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := s.cfg.Vendors["testvendor"].Endpoints["prod"]
	if len(got.Models) != 2 || got.Models[0] != "new-model-a" {
		t.Errorf("models should be updated when provided, got %v", got.Models)
	}
	if got.APIKey != "sk-keep" {
		t.Errorf("APIKey must still be preserved, got %q", got.APIKey)
	}
}

func TestIssue5293_EndpointPutNewEndpointStoresAsProvided(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/newep",
		`{"base_url":"https://fresh.example.com","protocol":"anthropic","max_tokens":8192}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "newep")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := s.cfg.Vendors["testvendor"].Endpoints["newep"]
	if got.BaseURL != "https://fresh.example.com" || got.Protocol != "anthropic" || got.MaxTokens != 8192 {
		t.Errorf("new endpoint must store body as provided, got %+v", got)
	}
}

func TestIssue5293_EndpointPutResponseMasksAPIKey(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	seed := config.EndpointConfig{
		Protocol: "openai",
		BaseURL:  "https://api.test.com",
		APIKey:   "sk-super-secret-5293",
	}
	s.cfg.Vendors["testvendor"].Endpoints["prod"] = seed

	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/prod",
		`{"base_url":"https://api.test.com","protocol":"openai"}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if key, present := resp["api_key"]; present && key != "***" {
		t.Errorf("response must mask preserved api_key, got %v", key)
	}
	if w.Body.String() == "" || !json.Valid(w.Body.Bytes()) {
		t.Fatal("response must remain valid JSON")
	}
}
