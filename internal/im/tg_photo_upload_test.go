package im

import (
	"bytes"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

func parsePhotoBody(t *testing.T, buf *bytes.Buffer, contentType string) *multipart.Form {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", contentType, err)
	}
	r := multipart.NewReader(bytes.NewReader(buf.Bytes()), params["boundary"])
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("ReadForm: %v", err)
	}
	return form
}

// TestTGPhotoOversizeBoundary pins the local 10MB multipart photo gate at the
// exact boundary: a 10MB photo passes (proceeds to upload), 10MB+1 is
// rejected with the human-readable error before any HTTP traffic.
func TestTGPhotoOversizeBoundary(t *testing.T) {
	exact := make([]byte, 10<<20)
	if err := tgPhotoOversize(exact); err != nil {
		t.Fatalf("exactly-10MB photo must pass the gate, got %v", err)
	}
	err := tgPhotoOversize(append(exact, 0))
	if err == nil || !strings.Contains(err.Error(), "sendPhoto limit") {
		t.Fatalf("10MB+1 must be rejected locally, got %v", err)
	}
}

// TestTGReplyToID pins the reply-to resolution semantics shared with the JSON
// sendPhoto path: blank skips, parseInt tolerates whitespace, 0 disables the
// linkage, non-numeric and overflow values are rejected.
func TestTGReplyToID(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, false},
		{"   ", 0, false},
		{"123", 123, true},
		{" 123 ", 123, true},
		{"abc", 0, false},
		{"0", 0, false},
		{"-5", 0, false},
		{"99999999999999999999", 0, false}, // parseInt overflow
	}
	for _, tc := range tests {
		got, ok := tgReplyToID(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("tgReplyToID(%q) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTGWriteReplyToField(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()
	if err := writeReplyToField(w, " 123 "); err != nil {
		t.Fatalf("writeReplyToField: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r := multipart.NewReader(bytes.NewReader(buf.Bytes()), boundary)
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("ReadForm: %v", err)
	}
	if got := form.Value["reply_to_message_id"]; len(got) != 1 || got[0] != "123" {
		t.Fatalf("reply_to_message_id = %v, want [123]", got)
	}

	// Blank and unparseable references must not write the field at all.
	for _, replyTo := range []string{"", "   ", "abc", "0"} {
		var b2 bytes.Buffer
		w2 := multipart.NewWriter(&b2)
		boundary2 := w2.Boundary()
		if err := writeReplyToField(w2, replyTo); err != nil {
			t.Fatalf("writeReplyToField(%q): %v", replyTo, err)
		}
		if err := w2.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		r2 := multipart.NewReader(bytes.NewReader(b2.Bytes()), boundary2)
		form2, err := r2.ReadForm(1 << 20)
		if err != nil {
			t.Fatalf("ReadForm: %v", err)
		}
		if got, exists := form2.Value["reply_to_message_id"]; exists {
			t.Fatalf("replyTo %q must not write reply_to_message_id, got %v", replyTo, got)
		}
	}
}

// TestTGWritePhotoCaptionFields pins the caption field routing: markdown
// conversion + raw entities with no explicit parse mode, escaped legacy
// caption + parse_mode otherwise, 1024-rune truncation, and no fields at all
// for an empty caption.
func TestTGWritePhotoCaptionFields(t *testing.T) {
	tests := []struct {
		name           string
		parseMode      string
		caption        string
		wantCaption    string
		noCaptionField bool
		wantEntities   bool
		wantParseMode  bool
	}{
		{
			"empty caption", "", "", "", true, false, false,
		},
		{"plain no parse mode", "", "hello", "hello", false, false, false},
		{"markdown entities", "", "**bold**", "bold", false, true, false},
		{"legacy html", "HTML", "a<b&c", "a&lt;b&amp;c", false, false, true},
		{"legacy truncation", "HTML", strings.Repeat("汉", 1030), strings.Repeat("汉", 1024), false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &tgAdapter{parseMode: tc.parseMode}
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			boundary := w.Boundary()
			if err := a.writePhotoCaptionFields(w, tc.caption); err != nil {
				t.Fatalf("writePhotoCaptionFields: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			r := multipart.NewReader(bytes.NewReader(buf.Bytes()), boundary)
			form, err := r.ReadForm(1 << 20)
			if err != nil {
				t.Fatalf("ReadForm: %v", err)
			}
			if !tc.noCaptionField {
				if got := form.Value["caption"]; len(got) != 1 || got[0] != tc.wantCaption {
					t.Fatalf("caption = %q, want %q", got, tc.wantCaption)
				}
			} else if got, ok := form.Value["caption"]; ok {
				t.Fatalf("empty caption must not write the caption field, got %q", got)
			}
			if _, ok := form.Value["caption_entities"]; ok != tc.wantEntities {
				t.Fatalf("caption_entities presence = %v, want %v", ok, tc.wantEntities)
			}
			if got, ok := form.Value["parse_mode"]; ok != tc.wantParseMode || (ok && got[0] != tc.parseMode) {
				t.Fatalf("parse_mode = %q (present=%v), want present=%v value %q", got, ok, tc.wantParseMode, tc.parseMode)
			}
		})
	}
}

// TestTGBuildPhotoUploadBody pins the full multipart envelope: chat_id field,
// photo file part with the caller's filename and raw bytes, and reply linkage.
func TestTGBuildPhotoUploadBody(t *testing.T) {
	a := &tgAdapter{}
	up := tgPhotoUpload{
		chatID:   "100",
		data:     []byte("fakepng"),
		filename: "img.png",
		caption:  "**bold**",
		replyTo:  "42",
	}
	buf, contentType, err := a.buildPhotoUploadBody(up)
	if err != nil {
		t.Fatalf("buildPhotoUploadBody: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Fatalf("content type = %q, want multipart/form-data", contentType)
	}
	form := parsePhotoBody(t, buf, contentType)
	if got := form.Value["chat_id"]; len(got) != 1 || got[0] != "100" {
		t.Fatalf("chat_id = %v, want [100]", got)
	}
	files := form.File["photo"]
	if len(files) != 1 || files[0].Filename != "img.png" {
		t.Fatalf("photo part = %+v, want single img.png", files)
	}
	f, err := files[0].Open()
	if err != nil {
		t.Fatalf("open photo part: %v", err)
	}
	defer f.Close()
	var got bytes.Buffer
	if _, err := got.ReadFrom(f); err != nil {
		t.Fatalf("read photo part: %v", err)
	}
	if got.String() != "fakepng" {
		t.Fatalf("photo content = %q, want fakepng", got.String())
	}
	if got := form.Value["reply_to_message_id"]; len(got) != 1 || got[0] != "42" {
		t.Fatalf("reply_to_message_id = %v, want [42]", got)
	}

	// Empty caption and blank replyTo produce a minimal envelope.
	up2 := tgPhotoUpload{chatID: "7", data: []byte("x"), filename: "y.png"}
	buf2, ct2, err := a.buildPhotoUploadBody(up2)
	if err != nil {
		t.Fatalf("buildPhotoUploadBody(minimal): %v", err)
	}
	form2 := parsePhotoBody(t, buf2, ct2)
	for _, absent := range []string{"caption", "caption_entities", "parse_mode", "reply_to_message_id"} {
		if got, ok := form2.Value[absent]; ok {
			t.Fatalf("minimal envelope must omit %s, got %q", absent, got)
		}
	}
}
