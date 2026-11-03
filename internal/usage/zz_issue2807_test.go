package usage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func issue2807BalanceServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
}

func TestIssue2807MultiCurrencyNoUSDFallsBackToFirst(t *testing.T) {
	srv := issue2807BalanceServer(t, `{"balance_infos":[{"currency":"CNY","total_balance":42.5},{"currency":"EUR","total_balance":7}]}`)
	defer srv.Close()
	info, err := DeepSeekProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance == nil || float64(*info.Balance) != 42.5 {
		t.Errorf("multi-currency no-USD must fall back to first entry (CNY 42.5), got %+v (#2807 recurrence)", info.Balance)
	}
}

func TestIssue2807USDStillPreferred(t *testing.T) {
	srv := issue2807BalanceServer(t, `{"balance_infos":[{"currency":"CNY","total_balance":88.5},{"currency":"USD","total_balance":12.34}]}`)
	defer srv.Close()
	info, err := DeepSeekProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance == nil || float64(*info.Balance) != 12.34 {
		t.Errorf("USD must be preferred, got %+v", info.Balance)
	}
}

func TestIssue2807SingleEntryAndEmpty(t *testing.T) {
	srv := issue2807BalanceServer(t, `{"balance_infos":[{"currency":"CNY","total_balance":3.25}]}`)
	defer srv.Close()
	info, err := DeepSeekProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil || info.Balance == nil || float64(*info.Balance) != 3.25 {
		t.Fatalf("single-entry must return its balance, got %+v err=%v", info.Balance, err)
	}

	srv2 := issue2807BalanceServer(t, `{"balance_infos":[]}`)
	defer srv2.Close()
	info2, err := DeepSeekProbe{}.Fetch(context.Background(), srv2.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info2.Balance != nil {
		t.Errorf("empty table must keep Balance=nil, got %+v", info2.Balance)
	}
}
