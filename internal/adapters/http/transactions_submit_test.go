package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"desafio/internal/app"
)

const testKey = "provider-a:transaction-123"

// postSubmit envia o corpo ao endpoint com a chave de idempotência válida.
func postSubmit(h http.Handler, body string) *httptest.ResponseRecorder {
	return doWithHeaders(h, http.MethodPost, "/wagering/transactions", body, map[string]string{IdempotencyHeader: testKey})
}

func submitBody(mutate func(string) string) string {
	body := `{"providerId":"provider-a","externalTransactionId":"transaction-123","playerId":"` + testUUID +
		`","walletId":"` + testUUID2 + `","roundId":"round-987","gameId":"fortune-chimp","kind":"BET",` +
		`"money":{"amount":"25.00","currency":"BRL"}}`
	if mutate != nil {
		body = mutate(body)
	}
	return body
}

func TestSubmitPassesInputToUseCase(t *testing.T) {
	bal := app.Money{Amount: "975.00", Currency: "BRL"}
	svc := &fakeTransactions{result: app.TransactionResult{TransactionID: testUUID, Status: app.StatusProcessed, Balance: &bal}}
	rec := postSubmit(newTestHandler(nil, svc), submitBody(nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[app.TransactionResult](t, rec)
	if got.Status != "PROCESSED" || got.Balance == nil || got.Balance.Amount != "975.00" || got.IdempotentReplay {
		t.Fatalf("resposta inesperada: %+v", got)
	}
	in := svc.submitIn
	if svc.calls != 1 || in.ProviderID != "provider-a" || in.Kind != "BET" || in.Money.Amount != "25.00" || in.WalletID != testUUID2 {
		t.Fatalf("entrada repassada: %+v (calls=%d)", in, svc.calls)
	}
}

func TestSubmitStatusMapping(t *testing.T) {
	tests := []struct {
		status string
		code   int
	}{
		{app.StatusProcessed, http.StatusOK},
		{app.StatusPending, http.StatusAccepted},
		{app.StatusPendingReference, http.StatusAccepted},
		{app.StatusRejected, http.StatusUnprocessableEntity},
		{app.StatusFailed, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		svc := &fakeTransactions{result: app.TransactionResult{TransactionID: testUUID, Status: tt.status, FailureCode: "X"}}
		rec := postSubmit(newTestHandler(nil, svc), submitBody(nil))
		if rec.Code != tt.code {
			t.Errorf("%s: status %d, esperado %d", tt.status, rec.Code, tt.code)
		}
		if got := decode[app.TransactionResult](t, rec); got.Status != tt.status || got.FailureCode != "X" {
			t.Errorf("%s: corpo inesperado %+v", tt.status, got)
		}
	}
}

func TestSubmitUseCaseErrors(t *testing.T) {
	rec := postSubmit(newTestHandler(nil, &fakeTransactions{err: app.ErrUnavailable}), submitBody(nil))
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "UNAVAILABLE" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
	rec = postSubmit(newTestHandler(nil, &fakeTransactions{err: app.ErrConflict}), submitBody(nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSubmitInvalidInput(t *testing.T) {
	replace := func(old, new string) func(string) string {
		return func(b string) string { return strings.Replace(b, old, new, 1) }
	}
	cases := map[string]string{
		"json inválido":           `{`,
		"sem providerId":          submitBody(replace(`"providerId":"provider-a",`, "")),
		"sem roundId":             submitBody(replace(`"roundId":"round-987",`, "")),
		"playerId":                submitBody(replace(testUUID, "abc")),
		"kind inválido":           submitBody(replace(`"BET"`, `"BOGUS"`)),
		"kind OPENING":            submitBody(replace(`"BET"`, `"OPENING"`)),
		"amount sem escala":       submitBody(replace(`"25.00"`, `"25"`)),
		"amount negativo":         submitBody(replace(`"25.00"`, `"-25.00"`)),
		"moeda":                   submitBody(replace(`"BRL"`, `"brl"`)),
		"campo desconhecido":      submitBody(replace(`"gameId"`, `"extra":1,"gameId"`)),
		"REFUND sem referência":   submitBody(replace(`"BET"`, `"REFUND"`)),
		"ROLLBACK sem referência": submitBody(replace(`"BET"`, `"ROLLBACK"`)),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeTransactions{}
			rec := postSubmit(newTestHandler(nil, svc), body)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
				t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
			}
			if svc.calls != 0 {
				t.Fatal("caso de uso não deveria ser chamado")
			}
		})
	}
}

func TestSubmitReversalWithReference(t *testing.T) {
	body := submitBody(func(b string) string {
		b = strings.Replace(b, `"BET"`, `"ROLLBACK"`, 1)
		return strings.Replace(b, `"gameId"`, `"referenceExternalTransactionId":"transaction-100","gameId"`, 1)
	})
	svc := &fakeTransactions{result: app.TransactionResult{Status: app.StatusPendingReference}}
	rec := postSubmit(newTestHandler(nil, svc), body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if svc.submitIn.ReferenceExternalTransactionID != "transaction-100" {
		t.Fatalf("referência não repassada: %+v", svc.submitIn)
	}
}

func TestSubmitIdempotencyKeyForwarded(t *testing.T) {
	svc := &fakeTransactions{result: app.TransactionResult{Status: app.StatusProcessed}}
	rec := postSubmit(newTestHandler(nil, svc), submitBody(nil))
	if rec.Code != http.StatusOK || svc.submitIn.IdempotencyKey != testKey {
		t.Fatalf("status %d, chave repassada %q", rec.Code, svc.submitIn.IdempotencyKey)
	}
}

func TestSubmitInvalidIdempotencyKey(t *testing.T) {
	keys := map[string]map[string]string{
		"ausente":     nil,
		"vazio":       {IdempotencyHeader: ""},
		"com espaço":  {IdempotencyHeader: "a b"},
		"muito longa": {IdempotencyHeader: strings.Repeat("k", 256)},
	}
	for name, headers := range keys {
		t.Run(name, func(t *testing.T) {
			svc := &fakeTransactions{}
			rec := doWithHeaders(newTestHandler(nil, svc), http.MethodPost, "/wagering/transactions", submitBody(nil), headers)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
				t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
			}
			if svc.calls != 0 {
				t.Fatal("caso de uso não deveria ser chamado")
			}
		})
	}
}

func TestSubmitIdempotencyConflicts(t *testing.T) {
	for err, code := range map[error]string{
		app.ErrIdempotencyConflict: "IDEMPOTENCY_CONFLICT",
		app.ErrInProgress:          "REQUEST_IN_PROGRESS",
	} {
		rec := postSubmit(newTestHandler(nil, &fakeTransactions{err: err}), submitBody(nil))
		if rec.Code != http.StatusConflict || errCode(t, rec) != code {
			t.Errorf("%v: status %d, corpo %s", err, rec.Code, rec.Body)
		}
	}
}

func TestSubmitReplayKeepsStatusAndBody(t *testing.T) {
	bal := app.Money{Amount: "975.00", Currency: "BRL"}
	svc := &fakeTransactions{result: app.TransactionResult{TransactionID: testUUID, Status: app.StatusProcessed, Balance: &bal, IdempotentReplay: true}}
	rec := postSubmit(newTestHandler(nil, svc), submitBody(nil))
	got := decode[app.TransactionResult](t, rec)
	if rec.Code != http.StatusOK || !got.IdempotentReplay || got.TransactionID != testUUID || got.Balance.Amount != "975.00" {
		t.Fatalf("status %d, corpo %+v", rec.Code, got)
	}
}
