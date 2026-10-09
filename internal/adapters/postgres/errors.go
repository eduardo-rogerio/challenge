package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/jackc/pgx/v5/pgconn"

	"desafio/internal/app"
)

// Nomes das constraints que têm significado de negócio.
const (
	constraintExternalKey = "wager_transactions_external_key"
)

// mapError traduz erros de infraestrutura do PostgreSQL para os erros da aplicação.
// Erros que já são da aplicação ou do domínio passam intactos.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505" && pgErr.ConstraintName == constraintExternalKey:
			return fmt.Errorf("%w: %v", app.ErrIdempotencyConflict, pgErr.Message)
		case pgErr.Code == "23505":
			return fmt.Errorf("%w: %v", app.ErrConflict, pgErr.Message)
		case unavailableCode(pgErr.Code):
			return fmt.Errorf("%w: %v", app.ErrUnavailable, pgErr.Message)
		}
		return err
	}
	var netErr net.Error
	var connErr *pgconn.ConnectError
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &netErr) || errors.As(err, &connErr) || pgconn.Timeout(err) {
		return fmt.Errorf("%w: %v", app.ErrUnavailable, err)
	}
	return err
}

// unavailableCode reconhece falhas temporárias: conexão (08), recursos (53), desligamento (57),
// lock_timeout (55P03), falha de serialização (40001) e deadlock (40P01).
func unavailableCode(code string) bool {
	switch code {
	case "55P03", "40001", "40P01":
		return true
	}
	return len(code) >= 2 && (code[:2] == "08" || code[:2] == "53" || code[:2] == "57")
}
