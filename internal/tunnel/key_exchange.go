package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const shareKeyExchangeContext = "ggcode-share-v3"

func generateShareKeyExchangeKeyPair() (publicHex, privateHex string, err error) {
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(privateKey.PublicKey().Bytes()), hex.EncodeToString(privateKey.Bytes()), nil
}

func wrapShareRoomKey(roomKey, roomID, clientID, serverPrivateHex, clientPublicHex string) (nonceB64, ciphertextB64 string, err error) {
	serverPrivate, err := decodeSharePrivateKey(serverPrivateHex)
	if err != nil {
		return "", "", err
	}
	clientPublic, err := decodeSharePublicKey(clientPublicHex)
	if err != nil {
		return "", "", err
	}
	sharedSecret, err := serverPrivate.ECDH(clientPublic)
	if err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(deriveShareWrapKey(sharedSecret, roomID, clientID))
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(roomKey), nil)
	return base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(ciphertext), nil
}

func unwrapShareRoomKey(nonceB64, ciphertextB64, roomID, clientID, clientPrivateHex, serverPublicHex string) (string, error) {
	clientPrivate, err := decodeSharePrivateKey(clientPrivateHex)
	if err != nil {
		return "", err
	}
	serverPublic, err := decodeSharePublicKey(serverPublicHex)
	if err != nil {
		return "", err
	}
	sharedSecret, err := clientPrivate.ECDH(serverPublic)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveShareWrapKey(sharedSecret, roomID, clientID))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func deriveShareWrapKey(sharedSecret []byte, roomID, clientID string) []byte {
	sum := sha256.Sum256([]byte(
		shareKeyExchangeContext + "\x00" + hex.EncodeToString(sharedSecret) + "\x00" + roomID + "\x00" + clientID,
	))
	return sum[:]
}

func decodeSharePrivateKey(raw string) (*ecdh.PrivateKey, error) {
	bytes, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode share private key: %w", err)
	}
	return ecdh.X25519().NewPrivateKey(bytes)
}

func decodeSharePublicKey(raw string) (*ecdh.PublicKey, error) {
	bytes, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode share public key: %w", err)
	}
	return ecdh.X25519().NewPublicKey(bytes)
}
