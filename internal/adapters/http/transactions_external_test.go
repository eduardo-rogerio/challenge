package httpadapter

import (
	"net/http"
	"testing"

	"desafio/internal/app"
)

const externalPath = "/providers/provider-a/wagering/transactions/transaction-123"

func TestGetTransactionByExternal(t *testing.T) {
	svc := &fakeTransactions{tx: app.Transaction{ID: testUUID, ProviderID: "provider-a", ExternalTransactionID: "transaction-123", Status: app.StatusProcessed}}
	rec := do(newTestHandler(nil, svc), http.MethodGet, externalPath, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := decode[app.Transaction](t, rec); got.ID != testUUID || got.Status != "PROCESSED" {
		t.Fatalf("resposta inesperada: %+v", got)
	}
	if svc.gotID != "provider-a" || svc.gotExt != "transaction-123" {
		t.Fatalf("par repassado = %q/%q", svc.gotID, svc.gotExt)
	}
}

func TestGetTransactionByExternalNotFound(t *testing.T) {
	rec := do(newTestHandler(nil, &fakeTransactions{err: app.ErrNotFound}), http.MethodGet, externalPath, "")
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
}
