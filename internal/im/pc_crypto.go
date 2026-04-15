package im

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const (
	pcSessionKeyBytes = 32
	pcIVBytes         = 12
	pcEnvelopeVersion = 1
)

var pcB64 = base64.URLEncoding.WithPadding(base64.NoPadding)

// pcGenerateSessionKey generates a base64url-encoded 32-byte random session key.
func pcGenerateSessionKey() (string, error) {
	key := make([]byte, pcSessionKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return pcB64.EncodeToString(key), nil
}

// pcDecodeSessionKey decodes a base64url session key into raw bytes.
func pcDecodeSessionKey(encoded string) ([]byte, error) {
	key, err := pcB64.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid session key base64: %w", err)
	}
	if len(key) != pcSessionKeyBytes {
		return nil, fmt.Errorf("session key must be %d bytes, got %d", pcSessionKeyBytes, len(key))
	}
	return key, nil
}

// pcEncryptPayload encrypts a payload and returns an EncryptedEnvelope.
func pcEncryptPayload(sessionID, sessionKeyB64 string, payload pcPayload) (*pcEncryptedEnvelope, error) {
	key, err := pcDecodeSessionKey(sessionKeyB64)
	if err != nil {
		return nil, err
	}

	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	iv := make([]byte, pcIVBytes)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// AAD = sessionId UTF-8 bytes
	sealed := aesgcm.Seal(nil, iv, plaintext, []byte(sessionID))

	// Go GCM Seal appends tag to ciphertext; split them.
	tagSize := aesgcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	tag := sealed[len(sealed)-tagSize:]

	messageID := pcBuildMessageID("frame")

	return &pcEncryptedEnvelope{
		Version:    pcEnvelopeVersion,
		MessageID:  messageID,
		IV:         pcB64.EncodeToString(iv),
		Ciphertext: pcB64.EncodeToString(ciphertext),
		Tag:        pcB64.EncodeToString(tag),
		SentAt:     pcNowISO(),
	}, nil
}

// pcDecryptPayload decrypts an EncryptedEnvelope and returns the payload.
func pcDecryptPayload(sessionID, sessionKeyB64 string, envelope *pcEncryptedEnvelope) (pcPayload, error) {
	if envelope.Version != pcEnvelopeVersion {
		return nil, fmt.Errorf("unsupported envelope version: %d", envelope.Version)
	}

	key, err := pcDecodeSessionKey(sessionKeyB64)
	if err != nil {
		return nil, err
	}

	iv, err := pcB64.DecodeString(envelope.IV)
	if err != nil {
		return nil, fmt.Errorf("decode iv: %w", err)
	}

	ciphertext, err := pcB64.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	tag, err := pcB64.DecodeString(envelope.Tag)
	if err != nil {
		return nil, fmt.Errorf("decode tag: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Go GCM Open expects ciphertext || tag concatenated.
	sealed := append(ciphertext, tag...)
	plaintext, err := aesgcm.Open(nil, iv, sealed, []byte(sessionID))
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	var payload pcPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return payload, nil
}
