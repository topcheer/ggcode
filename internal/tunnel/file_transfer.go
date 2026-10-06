package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

// Mobile file transfer V1 (docs/design/mobile-file-transfer.md).
//
// Transport: chunked inline over the existing E2E-encrypted WS channel - no
// gateway changes. Base64-in-JSON costs ~33%; a 512 KiB raw chunk keeps the
// encrypted envelope well under the 1 MiB WS read limit
// (relay_client.go SetReadLimit(1 << 20)).
const (
	// MobileFileChunkSize is the raw chunk size. Last chunk may be short.
	MobileFileChunkSize = 512 * 1024
	// MobileFileMaxSize is the single-file cap; larger files are rejected
	// locally with an explanatory message before any bytes are sent.
	MobileFileMaxSize = 50 * 1024 * 1024
	// MobileFileChunkDelay is the queue-yield rule: enqueue at most 1 chunk
	// per 50 ms so streaming interactive frames (text deltas) are not
	// starved behind a multi-chunk file burst.
	MobileFileChunkDelay = 50 * time.Millisecond
)

// FileOfferData announces a file before its chunks. Filename is display
// metadata only - the client MUST NOT resolve paths from it.
type FileOfferData struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"` // hex, over the whole raw file
	Chunks   int    `json:"chunks"` // ceil(size / 512KiB)
	Caption  string `json:"caption,omitempty"`
}

// FileChunkData carries one base64-encoded raw chunk.
type FileChunkData struct {
	FileID string `json:"file_id"`
	Index  int    `json:"index"`
	Data   string `json:"data"` // base64(raw chunk)
}

// FileDoneData is the optional terminator; success is inferred from chunks
// received + sha256 on the client, so a lost done event is not fatal.
type FileDoneData struct {
	FileID string `json:"file_id"`
}

// SendFileOffer enqueues a file_offer event. Idempotent per file_id: the
// client replaces any partial state for the same id (re-offer semantics).
// File events are UNRECORDED by design (#watchdog handoff intel): a 50 MiB
// multi-chunk transfer must not bump the projection/snapshot event feed -
// reconnect replay would otherwise re-stream every chunk of every file.
func (b *Broker) SendFileOffer(data FileOfferData) {
	b.enqueueUnrecorded(EventFileOffer, data)
}

// SendFileChunk enqueues one file_chunk event (unrecorded, see SendFileOffer).
func (b *Broker) SendFileChunk(data FileChunkData) {
	b.enqueueUnrecorded(EventFileChunk, data)
}

// SendFileDone enqueues the optional file_done terminator (unrecorded, see
// SendFileOffer).
func (b *Broker) SendFileDone(data FileDoneData) {
	b.enqueueUnrecorded(EventFileDone, data)
}

// enqueueUnrecorded marshals data and enqueues it WITHOUT recording into
// the projection/event feed (file-transfer bulk frames only).
func (b *Broker) enqueueUnrecorded(eventType string, data interface{}) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", eventType, err)
		return
	}
	b.enqueueWithBytes(eventType, "", dataBytes, false, false)
}

// MobileFileResult reports what a completed transfer sent.
type MobileFileResult struct {
	FileID   string
	Filename string
	Mime     string
	Size     int64
	Chunks   int
	SHA256   string
}

// SendFileToMobile reads the file at path, hashes it, sanitizes the
// filename, sniffs the MIME type, and drives offer → chunks → done on the
// broker with the 50 ms chunk-yield rule. ctx cancellation aborts between
// chunks (the enqueue path is non-blocking).
func (b *Broker) SendFileToMobile(ctx context.Context, filePath, caption string) (MobileFileResult, error) {
	offer, err := prepareFileOffer(filePath, caption)
	if err != nil {
		return MobileFileResult{}, err
	}
	if err := b.streamFileChunks(ctx, filePath, offer); err != nil {
		return MobileFileResult{}, err
	}
	b.SendFileDone(FileDoneData{FileID: offer.FileID})
	return MobileFileResult{
		FileID:   offer.FileID,
		Filename: offer.Filename,
		Mime:     offer.Mime,
		Size:     offer.Size,
		Chunks:   offer.Chunks,
		SHA256:   offer.SHA256,
	}, nil
}

// prepareFileOffer validates the file, computes the whole-file sha256 in a
// streaming pass, and builds the offer event. Validation failures reject
// locally before any bytes are sent (design contract).
func prepareFileOffer(filePath, caption string) (FileOfferData, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return FileOfferData{}, fmt.Errorf("stat %s: %w", filePath, err)
	}
	if fi.IsDir() {
		return FileOfferData{}, fmt.Errorf("%s is a directory; mobile file transfer accepts single files only", filePath)
	}
	if fi.Size() == 0 {
		return FileOfferData{}, fmt.Errorf("%s is empty; refusing to send an empty file", filePath)
	}
	if fi.Size() > MobileFileMaxSize {
		return FileOfferData{}, fmt.Errorf("file %s is %d bytes; the mobile transfer cap is %d bytes (50 MiB). Split the file or move it via a shared drive", filePath, fi.Size(), int64(MobileFileMaxSize))
	}

	f, err := os.Open(filePath)
	if err != nil {
		return FileOfferData{}, fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	sum, err := hashFile(f)
	if err != nil {
		return FileOfferData{}, err
	}
	return FileOfferData{
		FileID:   newMobileFileID(),
		Filename: SanitizeMobileFilename(path.Base(filePath)),
		Mime:     sniffMobileMIME(f, path.Base(filePath)),
		Size:     fi.Size(),
		SHA256:   sum,
		Chunks:   int((fi.Size() + int64(MobileFileChunkSize) - 1) / int64(MobileFileChunkSize)),
		Caption:  caption,
	}, nil
}

// streamFileChunks sends the offer, then streams each chunk with the yield
// rule. A second sequential read pass keeps memory flat (no full-file
// buffer on the host side); files are ≤50 MiB so this is cheap.
func (b *Broker) streamFileChunks(ctx context.Context, filePath string, offer FileOfferData) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	b.SendFileOffer(offer)
	buf := make([]byte, MobileFileChunkSize)
	for i := 0; i < offer.Chunks; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("mobile file transfer cancelled at chunk %d/%d: %w", i+1, offer.Chunks, err)
		}
		n, rerr := io.ReadFull(f, buf)
		if n <= 0 {
			if rerr == nil || rerr == io.EOF {
				break // short file vs. computed chunk count: trust the read
			}
			return fmt.Errorf("read %s chunk %d: %w", filePath, i, rerr)
		}
		b.SendFileChunk(FileChunkData{
			FileID: offer.FileID,
			Index:  i,
			Data:   base64.StdEncoding.EncodeToString(buf[:n]),
		})
		if i < offer.Chunks-1 {
			// Yield rule: at most 1 chunk per 50 ms.
			select {
			case <-ctx.Done():
				return fmt.Errorf("mobile file transfer cancelled: %w", ctx.Err())
			case <-time.After(MobileFileChunkDelay):
			}
		}
	}
	return nil
}

// SanitizeMobileFilename strips path separators and NUL, then clamps to 255
// UTF-8 bytes on a rune boundary. The result is display metadata only; the
// client never resolves paths from it.
func SanitizeMobileFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', '\x00':
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	for len(name) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		if size == 0 || size > len(name) {
			name = name[:255]
			break
		}
		name = name[:len(name)-size]
	}
	return name
}

// mobileMimeByExt is the extension fallback used when content sniffing
// returns the generic octet-stream default. No allowlist - "any format" is
// the requirement; unknown types stay octet-stream.
var mobileMimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".pdf": "application/pdf",
	".zip": "application/zip", ".txt": "text/plain; charset=utf-8",
	".log": "text/plain; charset=utf-8", ".json": "application/json",
	".mp4": "video/mp4", ".mp3": "audio/mpeg",
}

// sniffMobileMIME sniffs via http.DetectContentType on the first 512 bytes
// (it may read from f; callers pass an open file) with an extension
// fallback; application/octet-stream default.
func sniffMobileMIME(f *os.File, filename string) string {
	head := make([]byte, 512)
	n, err := f.ReadAt(head, 0)
	if err != nil && n == 0 {
		head = nil
	} else {
		head = head[:n]
	}
	if ct := http.DetectContentType(head); ct != "" && ct != "application/octet-stream" {
		return ct
	}
	if ct, ok := mobileMimeByExt[strings.ToLower(path.Ext(filename))]; ok {
		return ct
	}
	return "application/octet-stream"
}

// hashFile computes the hex sha256 of f in a streaming pass.
func hashFile(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// newMobileFileID returns a host-generated opaque hex id scoped to the
// session (16 random bytes).
func newMobileFileID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is effectively fatal; time-based fallback so
		// a transfer can still proceed with a unique-enough id.
		return fmt.Sprintf("f%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
