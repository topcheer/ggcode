//go:build !integration

package tunnel

import "testing"

func TestShareKeyExchangeWrapRoundTrip(t *testing.T) {
	serverPublic, serverPrivate, err := generateShareKeyExchangeKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	clientPublic, clientPrivate, err := generateShareKeyExchangeKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, err := wrapShareRoomKey("room-secret", "room-1", "client-1", serverPrivate, clientPublic)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := unwrapShareRoomKey(nonce, ciphertext, "room-1", "client-1", clientPrivate, serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "room-secret" {
		t.Fatalf("unwrapped room key = %q, want room-secret", plaintext)
	}
}
