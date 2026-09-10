package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// #1805 case 2: the renewal watcher's trigger is a pure disk predicate -
// oauth + expired + refreshable. Non-oauth, fresh, or unrefreshable
// credentials must not fire (keeping the ticker a no-op for everyone else).
func Test1805OAuthNeedsRenewal(t *testing.T) {
	store := auth.DefaultStore()
	original, origErr := store.Load(auth.ProviderAnthropic)
	defer func() {
		if original != nil {
			_ = store.Save(original)
		} else if origErr != nil {
			_ = store.Delete(auth.ProviderAnthropic)
		}
	}()

	fresh := &auth.Info{ProviderID: auth.ProviderAnthropic, Type: "oauth",
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}
	_ = store.Delete(auth.ProviderAnthropic)
	if err := store.Save(fresh); err != nil {
		t.Fatal(err)
	}
	if anthropicOAuthNeedsRenewal() {
		t.Fatal("fresh oauth token must not need renewal")
	}

	expired := &auth.Info{ProviderID: auth.ProviderAnthropic, Type: "oauth",
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}
	_ = store.Delete(auth.ProviderAnthropic)
	if err := store.Save(expired); err != nil {
		t.Fatal(err)
	}
	if !anthropicOAuthNeedsRenewal() {
		t.Fatal("expired+refreshable oauth token must need renewal")
	}

	noRefresh := &auth.Info{ProviderID: auth.ProviderAnthropic, Type: "oauth",
		AccessToken: "at", ExpiresAt: time.Now().Add(-time.Hour)}
	_ = store.Delete(auth.ProviderAnthropic)
	if err := store.Save(noRefresh); err != nil {
		t.Fatal(err)
	}
	if anthropicOAuthNeedsRenewal() {
		t.Fatal("expired token without refresh token must not trigger the watcher")
	}

	apiKey := &auth.Info{ProviderID: auth.ProviderAnthropic, Type: "api_key",
		AccessToken: "at", ExpiresAt: time.Now().Add(-time.Hour)}
	_ = store.Delete(auth.ProviderAnthropic)
	if err := store.Save(apiKey); err != nil {
		t.Fatal(err)
	}
	if anthropicOAuthNeedsRenewal() {
		t.Fatal("api-key credential must never trigger the oauth watcher")
	}
}
