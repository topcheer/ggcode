package config

import (
	"errors"
	"testing"
)

func Test1805PermanentRefreshClassification(t *testing.T) {
	if !isPermanentRefreshFailure(errors.New("oauth2: refresh failed: invalid_grant")) {
		t.Fatal("invalid_grant must be permanent")
	}
	if !isPermanentRefreshFailure(errors.New("400 {\"error\":\"invalid_grant\"}")) {
		t.Fatal("json-embedded invalid_grant must be permanent")
	}
	if isPermanentRefreshFailure(errors.New("dial tcp: i/o timeout")) {
		t.Fatal("network timeout must NOT be permanent")
	}
	if isPermanentRefreshFailure(errors.New("500 internal server error")) {
		t.Fatal("server error must NOT be permanent")
	}
	if isPermanentRefreshFailure(nil) {
		t.Fatal("nil must not be permanent")
	}
}
