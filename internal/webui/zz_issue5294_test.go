package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// zz_issue5294_test.go: PUT /api/vendors/{v} decodes api_key into a plain
// string, so "field absent" and "explicit empty" both arrived as "" and both
// went through SetVendorAPIKey(vendor, "") — destroying the vendor key (and
// its keys.env entry) on any partial update like {"display_name": "X"}.
// Fix: decode into *string; nil (absent) leaves the key unchanged.

func TestIssue5294_VendorPutAbsentAPIKeyPreservesKey(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	vc := s.cfg.Vendors["testvendor"]
	vc.APIKey = "${TEST_5294_VENDOR_KEY}"
	s.cfg.Vendors["testvendor"] = vc

	// Partial update per REST optional-field convention: no api_key field.
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor",
		`{"display_name":"Renamed Vendor"}`)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := s.cfg.Vendors["testvendor"]
	if got.DisplayName != "Renamed Vendor" {
		t.Errorf("display_name should be updated, got %q", got.DisplayName)
	}
	if got.APIKey != "${TEST_5294_VENDOR_KEY}" {
		t.Errorf("absent api_key must preserve vendor key, got %q", got.APIKey)
	}
}

func TestIssue5294_VendorPutSentinelPreservesKey(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	vc := s.cfg.Vendors["testvendor"]
	vc.APIKey = "${TEST_5294_VENDOR_KEY}"
	s.cfg.Vendors["testvendor"] = vc

	req := jsonBody(http.MethodPut, "/api/vendors/testvendor",
		`{"display_name":"Renamed Vendor","api_key":"__unchanged__"}`)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := s.cfg.Vendors["testvendor"].APIKey; got != "${TEST_5294_VENDOR_KEY}" {
		t.Errorf("sentinel api_key must preserve vendor key, got %q", got)
	}
}

func TestIssue5294_VendorPutExplicitEmptyClearsKey(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	vc := s.cfg.Vendors["testvendor"]
	vc.APIKey = "sk-plain-literal-5294" // plain literal: no ${ENV} ref side effects
	s.cfg.Vendors["testvendor"] = vc

	req := jsonBody(http.MethodPut, "/api/vendors/testvendor",
		`{"display_name":"X","api_key":""}`)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := s.cfg.Vendors["testvendor"].APIKey; strings.TrimSpace(got) != "" {
		t.Errorf("explicit empty api_key must clear vendor key, got %q", got)
	}
}
