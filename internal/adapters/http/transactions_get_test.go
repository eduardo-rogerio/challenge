package httpadapter

import (
	"net/http"
	"testing"

	"desafio/internal/app"
)

func TestGetTransaction(t *testing.T) {
	svc := &fakeTransactions{tx: app.Transaction{
		ID: testUUID, ProviderID: "provider-a", ExternalTransactionID: "transaction-123",
		Status: app.StatusRejected, FailureCode: "INSUFFICIENT_FUNDS",
	}}
	rec := do(newTestHandler(nil, svc), http.MethodGet, "/wagering/transactions/"+testUUID, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := decode[app.Transaction](t, rec); got.Status != "REJECTED" || got.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Fatalf("resposta inesperada: %+v", got)
	}
	if svc.gotID != testUUID {
		t.Fatalf("id repassado = %q", svc.gotID)
	}
}

func TestGetTransactionInvalidID(t *testing.T) {
	svc := &fakeTransactions{}
	rec := do(newTestHandler(nil, svc), http.MethodGet, "/wagering/transactions/abc", "")
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
	if svc.calls != 0 {
		t.Fatal("caso de uso não deveria ser chamado")
	}
}

func TestGetTransactionNotFound(t *testing.T) {
	rec := do(newTestHandler(nil, &fakeTransactions{err: app.ErrNotFound}), http.MethodGet, "/wagering/transactions/"+testUUID, "")
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
}
