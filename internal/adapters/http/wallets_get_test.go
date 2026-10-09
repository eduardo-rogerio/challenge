package httpadapter

import (
	"net/http"
	"testing"

	"desafio/internal/app"
)

func TestGetWallet(t *testing.T) {
	svc := &fakeWallets{wallet: app.Wallet{ID: testUUID2, PlayerID: testUUID, Balance: app.Money{Amount: "975.00", Currency: "BRL"}, Version: 2}}
	rec := do(newTestHandler(svc, nil), http.MethodGet, "/wallets/"+testUUID2, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := decode[app.Wallet](t, rec); got.Balance.Amount != "975.00" || got.Version != 2 {
		t.Fatalf("resposta inesperada: %+v", got)
	}
	if svc.gotID != testUUID2 {
		t.Fatalf("walletId repassado = %q", svc.gotID)
	}
}

func TestGetWalletInvalidID(t *testing.T) {
	svc := &fakeWallets{}
	rec := do(newTestHandler(svc, nil), http.MethodGet, "/wallets/abc", "")
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
	if svc.calls != 0 {
		t.Fatal("caso de uso não deveria ser chamado")
	}
}

func TestGetWalletNotFound(t *testing.T) {
	rec := do(newTestHandler(&fakeWallets{err: app.ErrNotFound}, nil), http.MethodGet, "/wallets/"+testUUID2, "")
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
}
