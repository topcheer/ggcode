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

const (
	MobileFileChunkSize = 512 * 1024

	MobileFileMaxSize = 50 * 1024 * 1024

	MobileFileChunkDelay = 50 * time.Millisecond
)

type FileOfferData struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Chunks   int    `json:"chunks"`
	Caption  string `json:"caption,omitempty"`
}

type FileChunkData struct {
	FileID string `json:"file_id"`
	Index  int    `json:"index"`
	Data   string `json:"data"`
}

type FileDoneData struct {
	FileID string `json:"file_id"`
}

func (b *Broker) SendFileOffer(data FileOfferData) {
	b.enqueueUnrecorded(EventFileOffer, data)
}

func (b *Broker) SendFileChunk(data FileChunkData) {
	b.enqueueUnrecorded(EventFileChunk, data)
}

func (b *Broker) SendFileDone(data FileDoneData) {
	b.enqueueUnrecorded(EventFileDone, data)
}

func (b *Broker) enqueueUnrecorded(eventType string, data interface{}) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", eventType, err)
		return
	}
	b.enqueueWithBytes(eventType, "", dataBytes, false, false)
}

type MobileFileResult struct {
	FileID   string
	Filename string
	Mime     string
	Size     int64
	Chunks   int
	SHA256   string
}

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
				break
			}
			return fmt.Errorf("read %s chunk %d: %w", filePath, i, rerr)
		}
		b.SendFileChunk(FileChunkData{
			FileID: offer.FileID,
			Index:  i,
			Data:   base64.StdEncoding.EncodeToString(buf[:n]),
		})
		if i < offer.Chunks-1 {

			select {
			case <-ctx.Done():
				return fmt.Errorf("mobile file transfer cancelled: %w", ctx.Err())
			case <-time.After(MobileFileChunkDelay):
			}
		}
	}
	return nil
}

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

var mobileMimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".pdf": "application/pdf",
	".zip": "application/zip", ".txt": "text/plain; charset=utf-8",
	".log": "text/plain; charset=utf-8", ".json": "application/json",
	".mp4": "video/mp4", ".mp3": "audio/mpeg",
}

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

func newMobileFileID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {

		return fmt.Sprintf("f%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
