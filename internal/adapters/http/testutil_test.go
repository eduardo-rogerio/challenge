package httpadapter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"desafio/internal/app"
)

const (
	testUUID  = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"
	testUUID2 = "0192f291-27dd-7d3f-8071-5f8685deef37"
)

// fakeWallets devolve valores fixos e registra a última chamada.
type fakeWallets struct {
	wallet app.Wallet
	page   app.LedgerPage
	rec    app.Reconciliation
	err    error

	calls    int
	openIn   app.OpenWalletInput
	gotID    string
	gotCurs  string
	gotLimit int
}

func (f *fakeWallets) Open(_ context.Context, in app.OpenWalletInput) (app.Wallet, error) {
	f.calls++
	f.openIn = in
	return f.wallet, f.err
}

func (f *fakeWallets) Get(_ context.Context, id string) (app.Wallet, error) {
	f.calls++
	f.gotID = id
	return f.wallet, f.err
}

func (f *fakeWallets) Ledger(_ context.Context, id, cursor string, limit int) (app.LedgerPage, error) {
	f.calls++
	f.gotID, f.gotCurs, f.gotLimit = id, cursor, limit
	return f.page, f.err
}

func (f *fakeWallets) Reconcile(_ context.Context, id string) (app.Reconciliation, error) {
	f.calls++
	f.gotID = id
	return f.rec, f.err
}

// fakeTransactions devolve valores fixos e registra a última chamada.
type fakeTransactions struct {
	result app.TransactionResult
	tx     app.Transaction
	err    error

	calls    int
	submitIn app.SubmitInput
	gotID    string
	gotExt   string
}

func (f *fakeTransactions) Submit(_ context.Context, in app.SubmitInput) (app.TransactionResult, error) {
	f.calls++
	f.submitIn = in
	return f.result, f.err
}

func (f *fakeTransactions) GetByID(_ context.Context, id string) (app.Transaction, error) {
	f.calls++
	f.gotID = id
	return f.tx, f.err
}

func (f *fakeTransactions) GetByExternal(_ context.Context, providerID, ext string) (app.Transaction, error) {
	f.calls++
	f.gotID, f.gotExt = providerID, ext
	return f.tx, f.err
}

func newTestHandler(w app.Wallets, t app.Transactions) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(log, http.NotFoundHandler(), nil, time.Second, Deps{Wallets: w, Transactions: t})
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	return doWithHeaders(h, method, path, body, nil)
}

func doWithHeaders(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("corpo de erro inválido %q: %v", rec.Body.String(), err)
	}
	return b.Error.Code
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("corpo inválido %q: %v", rec.Body.String(), err)
	}
	return v
}
