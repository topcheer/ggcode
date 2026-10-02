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

// newTestBroker returns a broker whose enqueued events are captured via the
// event recorder hook, so tests can assert offer/chunk framing without a
// live relay connection. The slice pointer lets the recorder closure append
// into the caller's variable.
// newFileTestBroker reuses the canonical no-senderLoop broker from
// broker_test.go: file events are unrecorded (no projection bump) so the
// event recorder never fires for them; tests assert on drained outbound
// messages instead.
func newFileTestBroker(t *testing.T) (*Broker, *[]GatewayMessage) {
	t.Helper()
	b, d := newBrokerForTest()
	var events []GatewayMessage
	_ = d
	return b, &events
}

// snapshotOutbound drains the outbound queue into sink (senderLoop is not
// running, so the queue only grows between explicit snapshots).
func (b *Broker) snapshotOutbound(sink *[]GatewayMessage) {
	b.outMu.Lock()
	*sink = append(*sink, b.outbound...)
	b.outMu.Unlock()
}

func TestSendFileToMobileFramingAndSHA256(t *testing.T) {
	// 1.2 MiB of distinguishable content → 3 chunks (512K, 512K, ~200K).
	raw := bytes.Repeat([]byte("abcdefg"), 200*1024) // 1,400,000 bytes
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

	// Expect: 1 offer + ceil(size/512K) chunks + 1 done.
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

	// Offer fields: whole-file sha256, size, chunk count, sanitized name.
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

	// Chunks: sequential indices, base64 payload reassembles to the file.
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
	// Build a sparse file claiming >50 MiB without allocating it.
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
	// 2 chunks → exactly one 50ms yield between them. With the yield the
	// transfer takes >= MobileFileChunkDelay; without it a 1 MiB in-memory
	// send completes in ~1ms.
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
	// Cancel while the first yield is pending: after chunk 0 is enqueued.
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	if _, err := b.SendFileToMobile(ctx, path, ""); err == nil {
		t.Fatal("cancelled transfer must error")
	}
	b.snapshotOutbound(eventsPtr)
	events := *eventsPtr
	// Offer + at most 1-2 chunks, never the full set nor file_done.
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
		{"../../etc/passwd", "....etcpasswd"},                // separators stripped; dots stay (legal filename chars, no traversal without separators)
		{`a\b\c.txt`, "abc.txt"},                             // windows separators
		{"na\x00me.bin", "name.bin"},                         // NUL stripped
		{"   ", "file"},                                      // whitespace-only → fallback
		{strings.Repeat("x", 300), strings.Repeat("x", 255)}, // clamp to 255 bytes
		{strings.Repeat("界", 100), strings.Repeat("界", 85)},  // clamp on rune boundary (85*3=255)
	}
	for _, c := range cases {
		if got := SanitizeMobileFilename(c.in); got != c.want {
			t.Errorf("SanitizeMobileFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSniffMobileMIME(t *testing.T) {
	dir := t.TempDir()
	// PNG magic → sniffed regardless of extension.
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
	// Unknown content, known extension → extension fallback.
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
	// Unknown content, unknown extension → octet-stream default.
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
	// msg.Data is already-marshaled JSON bytes (enqueueWithBytes stores
	// dataBytes); unmarshal directly, no round trip.
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

// openSparseForTest creates a file that reports the given size without
// allocating the bytes (seek-to-end + write-one-byte trick).
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

// TestFileEventsNotRecorded pins the unrecorded-path contract: file events
// must never reach the projection/event recorder, otherwise reconnect
// replay would re-stream every 512 KiB chunk of every transferred file.
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
