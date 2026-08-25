package im

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSignalSendExtractedImageDataURL pins the attachment path: a data-URL
// image must be POSTed to /v2/send with base64_attachments carrying the
// data:<mime>;filename=<name>;base64,<data> format from the
// signal-cli-rest-api docs.
func TestSignalSendExtractedImageDataURL(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v2/send") {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		body, _ := readAllForTest(r)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"timestamp":1700000000000}`))
	}))
	defer srv.Close()

	a := &signalAdapter{baseURL: srv.URL, conn: srv.Client()}
	err := a.sendExtractedImage(context.Background(), "+15551234567", ExtractedImage{
		Kind: "data_url",
		Data: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fakepng")),
	})
	if err != nil {
		t.Fatalf("sendExtractedImage: %v", err)
	}

	atts, ok := gotBody["base64_attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("expected 1 base64_attachment, got %v", gotBody["base64_attachments"])
	}
	att, _ := atts[0].(string)
	if !strings.HasPrefix(att, "data:image/png;filename=image.png;base64,") {
		t.Fatalf("bad attachment format: %q", att)
	}
	recips, _ := gotBody["recipients"].([]any)
	if len(recips) != 1 || recips[0] != "+15551234567" {
		t.Fatalf("expected direct recipient, got %v", gotBody["recipients"])
	}
}

// TestSignalSendExtractedImageLocalPath verifies local file routing: a bare
// local path (Kind "url") must be read from disk, decoded, and sent as an
// attachment - not fetched over HTTP.
func TestSignalSendExtractedImageLocalPath(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAllForTest(r)
		json.Unmarshal(body, &gotBody)
		w.Write([]byte(`{"timestamp":1}`))
	}))
	defer srv.Close()

	a := &signalAdapter{baseURL: srv.URL, conn: srv.Client()}
	err := a.sendExtractedImage(context.Background(), "group:abc", ExtractedImage{Kind: "url", Data: writeTempPNG(t)})
	if err != nil {
		t.Fatalf("sendExtractedImage local: %v", err)
	}

	atts, _ := gotBody["base64_attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %v", gotBody["base64_attachments"])
	}
	att, _ := atts[0].(string)
	if !strings.Contains(att, ";filename=img.png;") {
		t.Fatalf("expected img.png filename in %q", att)
	}
	recips, _ := gotBody["recipients"].([]any)
	if len(recips) != 1 {
		t.Fatalf("expected 1 recipient, got %v", gotBody["recipients"])
	}
	// group recipients use the double-encoded "group." prefix form
	first, ok := recips[0].(string)
	if !ok || !strings.HasPrefix(first, "group.") {
		t.Fatalf("expected group. prefix, got %v", recips)
	}
}

// TestSignalSendExtractedImageNonImage verifies non-image content is rejected.
func TestSignalSendExtractedImageNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer srv.Close()

	a := &signalAdapter{baseURL: srv.URL, conn: srv.Client()}
	err := a.sendExtractedImage(context.Background(), "+1555", ExtractedImage{Kind: "url", Data: srv.URL + "/x.png"})
	if err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Fatalf("expected non-image rejection, got %v", err)
	}
}
