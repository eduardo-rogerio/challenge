package httpadapter

import (
	"net/http"
	"testing"

	"desafio/internal/app"
)

func TestReconciliation(t *testing.T) {
	m := app.Money{Amount: "975.00", Currency: "BRL"}
	svc := &fakeWallets{rec: app.Reconciliation{
		WalletID: testUUID2, StoredBalance: m, CalculatedBalance: m,
		Difference: app.Money{Amount: "0.00", Currency: "BRL"}, Consistent: true, CheckedEntries: 2,
	}}
	rec := do(newTestHandler(svc, nil), http.MethodPost, "/wallets/"+testUUID2+"/reconciliation", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[app.Reconciliation](t, rec)
	if !got.Consistent || got.CheckedEntries != 2 || got.Difference.Amount != "0.00" {
		t.Fatalf("resposta inesperada: %+v", got)
	}
	if svc.gotID != testUUID2 {
		t.Fatalf("walletId repassado = %q", svc.gotID)
	}
}

func TestReconciliationInvalidID(t *testing.T) {
	svc := &fakeWallets{}
	rec := do(newTestHandler(svc, nil), http.MethodPost, "/wallets/abc/reconciliation", "")
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
	if svc.calls != 0 {
		t.Fatal("caso de uso não deveria ser chamado")
	}
}

func TestReconciliationNotFound(t *testing.T) {
	rec := do(newTestHandler(&fakeWallets{err: app.ErrNotFound}, nil), http.MethodPost, "/wallets/"+testUUID2+"/reconciliation", "")
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
}
