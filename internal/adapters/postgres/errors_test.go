package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"desafio/internal/app"
	"desafio/internal/domain"
)

func TestMapErrorUnavailable(t *testing.T) {
	for _, code := range []string{"08006", "08001", "53300", "57P01", "57P03", "55P03", "40001", "40P01"} {
		err := mapError(&pgconn.PgError{Code: code, Message: "x"})
		if !errors.Is(err, app.ErrUnavailable) {
			t.Errorf("SQLSTATE %s: %v não é ErrUnavailable", code, err)
		}
	}
	for name, e := range map[string]error{
		"deadline": context.DeadlineExceeded,
		"EOF":      io.EOF,
		"conexão":  &pgconn.ConnectError{},
	} {
		if err := mapError(fmt.Errorf("wrap: %w", e)); !errors.Is(err, app.ErrUnavailable) {
			t.Errorf("%s: %v não é ErrUnavailable", name, err)
		}
	}
}

func TestMapErrorUniqueViolations(t *testing.T) {
	ext := mapError(&pgconn.PgError{Code: "23505", ConstraintName: "wager_transactions_external_key"})
	if !errors.Is(ext, app.ErrIdempotencyConflict) {
		t.Errorf("operação externa: %v", ext)
	}
	other := mapError(&pgconn.PgError{Code: "23505", ConstraintName: "wallets_player_currency_key"})
	if !errors.Is(other, app.ErrConflict) || errors.Is(other, app.ErrIdempotencyConflict) {
		t.Errorf("carteira: %v", other)
	}
}

func TestMapErrorPassesThrough(t *testing.T) {
	if mapError(nil) != nil {
		t.Error("nil deve continuar nil")
	}
	for _, e := range []error{app.ErrNotFound, domain.ErrInsufficientFunds, errors.New("outro"), &pgconn.PgError{Code: "23514"}} {
		if err := mapError(e); err != e {
			t.Errorf("%v deveria passar intacto, obtido %v", e, err)
		}
	}
}
