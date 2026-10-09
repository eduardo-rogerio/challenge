package httpadapter

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"desafio/internal/app"
)

func TestWriteErrorMapping(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("x: %w", app.ErrInvalidInput), 400, "INVALID_REQUEST"},
		{app.ErrNotFound, 404, "NOT_FOUND"},
		{fmt.Errorf("wrap: %w", app.ErrConflict), 409, "CONFLICT"},
		{app.ErrIdempotencyConflict, 409, "IDEMPOTENCY_CONFLICT"},
		{fmt.Errorf("wrap: %w", app.ErrInProgress), 409, "REQUEST_IN_PROGRESS"},
		{app.ErrUnavailable, 503, "UNAVAILABLE"},
		{app.ErrNotImplemented, 501, "NOT_IMPLEMENTED"},
		{errors.New("senha=segredo"), 500, "INTERNAL"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		writeError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tt.err)
		if rec.Code != tt.status {
			t.Errorf("%v: status %d, esperado %d", tt.err, rec.Code, tt.status)
		}
		if got := errCode(t, rec); got != tt.code {
			t.Errorf("%v: code %q, esperado %q", tt.err, got, tt.code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type %q", ct)
		}
		if strings.Contains(rec.Body.String(), "segredo") {
			t.Error("mensagem do erro interno vazou na resposta")
		}
	}
}
