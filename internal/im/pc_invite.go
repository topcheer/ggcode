package im

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const PCInviteURIPrefix = "privateclaw://connect?payload="

func PCEncodeInviteToURI(invite PCInvite) (string, error) {
	data, err := json.Marshal(invite)
	if err != nil {
		return "", err
	}
	encoded := pcB64.EncodeToString(data)
	return PCInviteURIPrefix + encoded, nil
}

func pcDecodeInviteString(input string) (*PCInvite, error) {
	trimmed := strings.TrimSpace(input)

	if strings.HasPrefix(trimmed, "privateclaw://") {
		idx := strings.Index(trimmed, "payload=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid invite URI: missing payload parameter")
		}
		b64 := trimmed[idx+len("payload="):]

		if ampIdx := strings.IndexByte(b64, '&'); ampIdx >= 0 {
			b64 = b64[:ampIdx]
		}
		if hashIdx := strings.IndexByte(b64, '#'); hashIdx >= 0 {
			b64 = b64[:hashIdx]
		}
		return pcDecodeInviteFromBase64(b64)
	}

	if strings.HasPrefix(trimmed, "{") {
		var invite PCInvite
		if err := json.Unmarshal([]byte(trimmed), &invite); err != nil {
			return nil, fmt.Errorf("invalid invite JSON: %w", err)
		}
		return &invite, nil
	}

	return pcDecodeInviteFromBase64(trimmed)
}

func pcDecodeInviteFromBase64(b64 string) (*PCInvite, error) {
	data, err := pcB64.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("invalid invite base64: %w", err)
	}
	var invite PCInvite
	if err := json.Unmarshal(data, &invite); err != nil {
		return nil, fmt.Errorf("invalid invite payload: %w", err)
	}
	return &invite, nil
}

func pcBuildMessageID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

func pcNowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func pcGenerateRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("req-%x", b)
}
