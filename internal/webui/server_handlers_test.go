package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVendorPUTKeepsDisplayNameWithKeyChange(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	vendor := "testvendor"

	body := `{"api_key":"sk-new","display_name":"New Name"}`
	req := httptest.NewRequest(http.MethodPut, "/api/vendors/"+vendor, strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleVendorDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT failed: %d %s", rec.Code, rec.Body.String())
	}
	if got := s.cfg.Vendors[vendor].DisplayName; got != "New Name" {
		t.Fatalf("display name lost: %q (want %q)", got, "New Name")
	}
}

func TestGeneralPUTOmittedMaxIterationsNoOp(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	s.cfg.MaxIterations = 40

	req := httptest.NewRequest(http.MethodPut, "/api/general", strings.NewReader(`{"language":"en"}`))
	rec := httptest.NewRecorder()
	s.handleGeneral(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT failed: %d %s", rec.Code, rec.Body.String())
	}
	if s.cfg.MaxIterations != 40 {
		t.Fatalf("omitted max_iterations overwrote limit: got %d want 40", s.cfg.MaxIterations)
	}
}
