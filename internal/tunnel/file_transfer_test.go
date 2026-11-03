package tunnel

import (
	"encoding/json"
	"os"

	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func newFileTestBroker(t *testing.T) (*Broker, *[]GatewayMessage) {
	t.Helper()
	b, d := newBrokerForTest()
	var events []GatewayMessage
	_ = d
	return b, &events
}

func (b *Broker) snapshotOutbound(sink *[]GatewayMessage) {
	b.outMu.Lock()
	*sink = append(*sink, b.outbound...)
	b.outMu.Unlock()
}

func TestSendFileToMobileFramingAndSHA256(t *testing.T) {

	raw := bytes.Repeat([]byte("abcdefg"), 200*1024)
	dir := t.TempDir()
	path := dir + "/sample.bin"
	if err := writeFileForTest(path, raw); err != nil {
		t.Fatal(err)
	}

	b, eventsPtr := newFileTestBroker(t)
	res, err := b.SendFileToMobile(context.Background(), path, "hello")
	if err != nil {
		t.Fatalf("SendFileToMobile: %v", err)
	}
	b.snapshotOutbound(eventsPtr)
	events := *eventsPtr

	wantChunks := (len(raw) + MobileFileChunkSize - 1) / MobileFileChunkSize
	if res.Chunks != wantChunks {
		t.Fatalf("chunks = %d, want %d", res.Chunks, wantChunks)
	}
	if got := len(events); got != wantChunks+2 {
		t.Fatalf("events = %d, want %d (offer+chunks+done)", got, wantChunks+2)
	}
	if events[0].Type != EventFileOffer {
		t.Fatalf("first event = %q, want file_offer", events[0].Type)
	}
	if events[len(events)-1].Type != EventFileDone {
		t.Fatalf("last event = %q, want file_done", events[len(events)-1].Type)
	}

	sum := sha256.Sum256(raw)
	var offer FileOfferData
	decodeEvent(t, events[0], &offer)
	if offer.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("offer sha256 = %s, want %s", offer.SHA256, hex.EncodeToString(sum[:]))
	}
	if offer.Size != int64(len(raw)) {
		t.Fatalf("offer size = %d, want %d", offer.Size, len(raw))
	}
	if offer.Chunks != wantChunks {
		t.Fatalf("offer chunks = %d, want %d", offer.Chunks, wantChunks)
	}
	if offer.Filename != "sample.bin" {
		t.Fatalf("offer filename = %q", offer.Filename)
	}
	if offer.Caption != "hello" {
		t.Fatalf("offer caption = %q, want hello", offer.Caption)
	}
	if offer.FileID == "" || len(offer.FileID) != 32 {
		t.Fatalf("offer file_id = %q, want 16-byte hex", offer.FileID)
	}

	var assembled []byte
	for i, ev := range events[1 : len(events)-1] {
		if ev.Type != EventFileChunk {
			t.Fatalf("event %d type = %q, want file_chunk", i+1, ev.Type)
		}
		var chunk FileChunkData
		decodeEvent(t, ev, &chunk)
		if chunk.FileID != offer.FileID {
			t.Fatalf("chunk %d file_id mismatch", i)
		}
		if chunk.Index != i {
			t.Fatalf("chunk index = %d, want %d", chunk.Index, i)
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatalf("chunk %d base64: %v", i, err)
		}
		assembled = append(assembled, data...)
	}
	if !bytes.Equal(assembled, raw) {
		t.Fatalf("reassembled %d bytes != original %d bytes", len(assembled), len(raw))
	}
}

func TestSendFileToMobileRejectsOversize(t *testing.T) {

	dir := t.TempDir()
	path := dir + "/big.bin"
	f, err := openSparseForTest(path, int64(MobileFileMaxSize)+1)
	if err != nil {
		t.Skipf("sparse file unsupported: %v", err)
	}
	f.Close()

	b, eventsPtr := newFileTestBroker(t)
	_, err = b.SendFileToMobile(context.Background(), path, "")
	if err == nil {
		t.Fatal("oversize file must be rejected")
	}
	if !strings.Contains(err.Error(), "50 MiB") {
		t.Fatalf("rejection must explain the cap: %v", err)
	}
	b.snapshotOutbound(eventsPtr)
	if got := len(*eventsPtr); got != 0 {
		t.Fatalf("rejection must send zero events, got %d", got)
	}
}

func TestSendFileToMobileRejectsDirAndMissing(t *testing.T) {
	b, _ := newFileTestBroker(t)
	dir := t.TempDir()
	if _, err := b.SendFileToMobile(context.Background(), dir, ""); err == nil {
		t.Fatal("directory must be rejected")
	}
	if _, err := b.SendFileToMobile(context.Background(), dir+"/nope", ""); err == nil {
		t.Fatal("missing file must be rejected")
	}
}

func TestSendFileToMobileYieldRule(t *testing.T) {

	dir := t.TempDir()
	path := dir + "/two.bin"
	raw := bytes.Repeat([]byte{0xAA}, MobileFileChunkSize+1024)
	if err := writeFileForTest(path, raw); err != nil {
		t.Fatal(err)
	}

	b, _ := newFileTestBroker(t)
	start := time.Now()
	if _, err := b.SendFileToMobile(context.Background(), path, ""); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < MobileFileChunkDelay {
		t.Fatalf("yield rule violated: 2-chunk transfer took %v, want >= %v", elapsed, MobileFileChunkDelay)
	}
}

func TestSendFileToMobileCancelBetweenChunks(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cancel.bin"
	raw := bytes.Repeat([]byte{0xBB}, 3*MobileFileChunkSize)
	if err := writeFileForTest(path, raw); err != nil {
		t.Fatal(err)
	}

	b, eventsPtr := newFileTestBroker(t)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	if _, err := b.SendFileToMobile(ctx, path, ""); err == nil {
		t.Fatal("cancelled transfer must error")
	}
	b.snapshotOutbound(eventsPtr)
	events := *eventsPtr

	if len(events) >= 3+2 {
		t.Fatalf("cancelled transfer sent %d events, expected partial", len(events))
	}
	for _, ev := range events {
		if ev.Type == EventFileDone {
			t.Fatal("file_done must not be sent after cancellation")
		}
	}
}

func TestSanitizeMobileFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"report.pdf", "report.pdf"},
		{"../../etc/passwd", "....etcpasswd"},
		{`a\b\c.txt`, "abc.txt"},
		{"na\x00me.bin", "name.bin"},
		{"   ", "file"},
		{strings.Repeat("x", 300), strings.Repeat("x", 255)},
		{strings.Repeat("界", 100), strings.Repeat("界", 85)},
	}
	for _, c := range cases {
		if got := SanitizeMobileFilename(c.in); got != c.want {
			t.Errorf("SanitizeMobileFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSniffMobileMIME(t *testing.T) {
	dir := t.TempDir()

	pngPath := dir + "/a.dat"
	if err := writeFileForTest(pngPath, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	f, err := openReadForTest(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := sniffMobileMIME(f, "a.dat"); got != "image/png" {
		t.Fatalf("sniff png = %q", got)
	}

	zipPath := dir + "/b.zip"
	if err := writeFileForTest(zipPath, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	f2, err := openReadForTest(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	if got := sniffMobileMIME(f2, "b.zip"); got != "application/zip" {
		t.Fatalf("ext fallback = %q", got)
	}

	f3, err := openReadForTest(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f3.Close()
	if got := sniffMobileMIME(f3, "c.weird"); got != "application/octet-stream" {
		t.Fatalf("default = %q", got)
	}
}

func decodeEvent(t *testing.T, msg GatewayMessage, v interface{}) {
	t.Helper()

	if err := json.Unmarshal(msg.Data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func writeFileForTest(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func openReadForTest(path string) (*os.File, error) {
	return os.Open(path)
}

func openSparseForTest(path string, size int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func TestFileEventsNotRecorded(t *testing.T) {
	b, _ := newFileTestBroker(t)
	var recorded int
	b.SetEventRecorder(func(msg GatewayMessage) { recorded++ })

	dir := t.TempDir()
	path := dir + "/pin.bin"
	if err := writeFileForTest(path, []byte("tiny")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendFileToMobile(context.Background(), path, ""); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatalf("file events must not be recorded, recorder saw %d events", recorded)
	}
}
