package platform

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJWTSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret-0123456789abcdef")
	tok, err := SignToken(secret, "alice", time.Hour)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	claims, err := VerifyToken(secret, tok)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if claims.Sub != "alice" {
		t.Fatalf("Sub = %q, want alice", claims.Sub)
	}
	if claims.Exp <= time.Now().Unix() {
		t.Fatalf("Exp %d not in the future", claims.Exp)
	}
}

func TestJWTRejectWrongSecret(t *testing.T) {
	tok, _ := SignToken([]byte("secret-a"), "bob", time.Hour)
	if _, err := VerifyToken([]byte("secret-b"), tok); err != ErrBadToken {
		t.Fatalf("want ErrBadToken, got %v", err)
	}
}

func TestJWTRejectExpired(t *testing.T) {
	secret := []byte("k")
	tok, err := signClaims(secret, Claims{Sub: "bob", Iat: 1, Exp: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyToken(secret, tok); err != ErrBadToken {
		t.Fatalf("want ErrBadToken, got %v", err)
	}
}

func TestJWTRejectTamperedPayload(t *testing.T) {
	secret := []byte("k")
	tok, _ := SignToken(secret, "bob", time.Hour)
	parts := strings.Split(tok, ".")
	// swap subject in the payload
	var claims Claims
	body, _ := b64Decode(parts[1])
	_ = json.Unmarshal(body, &claims)
	claims.Sub = "admin"
	newBody, _ := json.Marshal(claims)
	tampered := parts[0] + "." + b64Raw(newBody) + "." + parts[2]
	if _, err := VerifyToken(secret, tampered); err != ErrBadToken {
		t.Fatalf("want ErrBadToken, got %v", err)
	}
}

func TestJWTRejectAlgConfusion(t *testing.T) {
	secret := []byte("k")
	tok, _ := SignToken(secret, "bob", time.Hour)
	parts := strings.Split(tok, ".")
	// craft a none-alg header
	none := b64Raw([]byte("{\"alg\":\"none\",\"typ\":\"JWT\"}"))
	parts[0] = none
	if _, err := VerifyToken(secret, strings.Join(parts, ".")); err != ErrBadToken {
		t.Fatalf("want ErrBadToken for alg=none, got %v", err)
	}
}

func TestJWTRandomIDLengthAndCharset(t *testing.T) {
	id, err := RandomID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 16 || !validJobID(id) {
		t.Fatalf("bad random id %q", id)
	}
}

func TestValidJobIDRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"../../etc/passwd", "..", "", "AAAAAAAAAAAAAAAA", "abc"} {
		if validJobID(bad) {
			t.Errorf("validJobID(%q) = true, want false", bad)
		}
	}
}
