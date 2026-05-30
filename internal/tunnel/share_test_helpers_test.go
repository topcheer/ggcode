//go:build !integration

package tunnel

import "testing"

func testShareDescriptor(t testing.TB) ShareDescriptor {
	t.Helper()
	serverPub, serverPriv, err := generateShareKeyExchangeKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return ShareDescriptor{
		ProtocolVersion:  ShareProtocolV3,
		ShareMode:        ShareModeV3,
		RoomID:           "room-test",
		AuthTicket:       "server-auth",
		RenewToken:       "server-renew",
		CryptoKey:        "room-secret",
		ServerPublicKey:  serverPub,
		ServerPrivateKey: serverPriv,
	}
}

func testRelayClient(t testing.TB, relayURL string) *RelayClient {
	t.Helper()
	rc, err := NewRelayClientWithDescriptor(relayURL, testShareDescriptor(t), "server", RelayClientMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func mustTestRelayClient(relayURL string) *RelayClient {
	desc := ShareDescriptor{
		ProtocolVersion:  ShareProtocolV3,
		ShareMode:        ShareModeV3,
		RoomID:           "room-test",
		AuthTicket:       "server-auth",
		RenewToken:       "server-renew",
		CryptoKey:        "room-secret",
		ServerPublicKey:  "test-server-pub",
		ServerPrivateKey: "test-server-priv",
	}
	rc, err := NewRelayClientWithDescriptor(relayURL, desc, "server", RelayClientMetadata{})
	if err != nil {
		panic(err)
	}
	return rc
}
