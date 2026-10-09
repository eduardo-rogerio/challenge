package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"desafio/internal/app"
)

func TestLedgerDefaults(t *testing.T) {
	svc := &fakeWallets{}
	rec := do(newTestHandler(svc, nil), http.MethodGet, "/wallets/"+testUUID2+"/ledger", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if svc.gotLimit != 50 || svc.gotCurs != "" || svc.gotID != testUUID2 {
		t.Fatalf("parâmetros: id=%q cursor=%q limit=%d", svc.gotID, svc.gotCurs, svc.gotLimit)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"entries":[]`) || !strings.Contains(body, `"nextCursor":null`) {
		t.Fatalf("corpo inesperado: %s", body)
	}
}

func TestLedgerCursorAndLimitForwarded(t *testing.T) {
	next := "abc"
	svc := &fakeWallets{page: app.LedgerPage{
		Entries:    []app.LedgerEntry{{ID: testUUID, Direction: "DEBIT"}},
		NextCursor: &next,
	}}
	rec := do(newTestHandler(svc, nil), http.MethodGet, "/wallets/"+testUUID2+"/ledger?cursor=xyz&limit=10", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if svc.gotCurs != "xyz" || svc.gotLimit != 10 {
		t.Fatalf("cursor=%q limit=%d", svc.gotCurs, svc.gotLimit)
	}
	got := decode[app.LedgerPage](t, rec)
	if len(got.Entries) != 1 || got.NextCursor == nil || *got.NextCursor != "abc" {
		t.Fatalf("página inesperada: %+v", got)
	}
}

func TestLedgerInvalidParams(t *testing.T) {
	for _, path := range []string{
		"/wallets/abc/ledger",
		"/wallets/" + testUUID2 + "/ledger?limit=0",
		"/wallets/" + testUUID2 + "/ledger?limit=101",
		"/wallets/" + testUUID2 + "/ledger?limit=x",
	} {
		svc := &fakeWallets{}
		rec := do(newTestHandler(svc, nil), http.MethodGet, path, "")
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
			t.Errorf("%s: status %d, corpo %s", path, rec.Code, rec.Body)
		}
		if svc.calls != 0 {
			t.Errorf("%s: caso de uso não deveria ser chamado", path)
		}
	}
}

func TestLedgerNotFound(t *testing.T) {
	rec := do(newTestHandler(&fakeWallets{err: app.ErrNotFound}, nil), http.MethodGet, "/wallets/"+testUUID2+"/ledger", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}
