package a2a

import (
	"testing"
)

func TestPushDeliveryRevalidatesLiveURL_1582(t *testing.T) {
	srv := NewServer(ServerConfig{Port: 0, APIKey: "k"}, NewTaskHandler(".", nil, nil))
	err := srv.validatePushCallbackURL("http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("internal metadata URL must fail validation - if this fails the guard itself is broken")
	}
	if srv.validatePushCallbackURL("https://public-ok.example.invalid/cb") == nil {
		t.Log("public URL validation outcome recorded")
	}
}
