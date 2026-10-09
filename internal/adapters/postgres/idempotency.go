package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desafio/internal/app"
)

// IdempotencyStore implementa app.IdempotencyStore sobre a tabela idempotency_keys.
// A lease usa o relógio do banco, para não depender do relógio de cada instância.
type IdempotencyStore struct {
	pool  *pgxpool.Pool
	lease time.Duration
}

var _ app.IdempotencyStore = (*IdempotencyStore)(nil)

func NewIdempotencyStore(pool *pgxpool.Pool, lease time.Duration) *IdempotencyStore {
	return &IdempotencyStore{pool: pool, lease: lease}
}

const insertKey = `
INSERT INTO idempotency_keys (key, provider_id, external_transaction_id, request_hash, state, locked_until)
VALUES ($1, $2, $3, $4, 'IN_PROGRESS', now() + make_interval(secs => $5))
ON CONFLICT DO NOTHING`

const selectKey = `
SELECT request_hash, state, response, locked_until < now()
FROM idempotency_keys WHERE key = $1 FOR UPDATE`

// Begin tenta registrar a chave; ver app.IdempotencyStore.
func (s *IdempotencyStore) Begin(ctx context.Context, key, providerID, externalID, hash string) (*app.TransactionResult, error) {
	// A linha pode desaparecer entre o INSERT e o SELECT (Release concorrente): uma repetição basta.
	for range 2 {
		res, retry, err := s.begin(ctx, key, providerID, externalID, hash)
		if !retry {
			return res, mapError(err)
		}
	}
	// Sem linha para a chave: o par (provedor, id externo) já pertence a outra chave.
	return nil, app.ErrIdempotencyConflict
}

func (s *IdempotencyStore) begin(ctx context.Context, key, providerID, externalID, hash string) (res *app.TransactionResult, retry bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, insertKey, key, providerID, externalID, hash, s.lease.Seconds())
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return nil, false, tx.Commit(ctx)
	}

	var (
		storedHash, state string
		response          []byte
		expired           bool
	)
	err = tx.QueryRow(ctx, selectKey, key).Scan(&storedHash, &state, &response, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}

	switch {
	case storedHash != hash:
		return nil, false, app.ErrIdempotencyConflict
	case state == "COMPLETED":
		var out app.TransactionResult
		if err := json.Unmarshal(response, &out); err != nil {
			return nil, false, fmt.Errorf("resultado persistido inválido: %w", err)
		}
		return &out, false, nil
	case !expired:
		return nil, false, app.ErrInProgress
	}

	// Lease vencida: a execução anterior foi abandonada; esta requisição a assume.
	if _, err := tx.Exec(ctx,
		`UPDATE idempotency_keys SET locked_until = now() + make_interval(secs => $2), updated_at = now() WHERE key = $1`,
		key, s.lease.Seconds()); err != nil {
		return nil, false, err
	}
	return nil, false, tx.Commit(ctx)
}

func (s *IdempotencyStore) Complete(ctx context.Context, key string, result app.TransactionResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE idempotency_keys
		 SET state = 'COMPLETED', response = $2, locked_until = now(), updated_at = now()
		 WHERE key = $1 AND state = 'IN_PROGRESS'`, key, body)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("idempotência: chave %q não está em andamento", key)
	}
	return nil
}

func (s *IdempotencyStore) Release(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE key = $1 AND state = 'IN_PROGRESS'`, key)
	return mapError(err)
}
