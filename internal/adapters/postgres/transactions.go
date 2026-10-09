package postgres

import (
	"context"
	"fmt"
	"time"

	"desafio/internal/app"
	"desafio/internal/domain"
)

type transactionRepo struct{ db dbtx }

const transactionColumns = `id::text, wallet_id::text, player_id::text, kind, amount, currency, status, failure_code,
	result_balance, provider_id, external_transaction_id, idempotency_key, request_hash, round_id, game_id,
	reference_external_transaction_id, resolved_reference_id::text, created_at, updated_at`

// nullable devolve nil para texto vazio, gravando NULL nas colunas opcionais (OPENING não tem metadados externos).
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r transactionRepo) Insert(ctx context.Context, tx *domain.WagerTransaction) error {
	var resultBalance *int64
	if b, ok := tx.ResultBalance(); ok {
		v := b.Minor()
		resultBalance = &v
	}
	// A pendência fica elegível já no próximo ciclo do worker; o intervalo de varredura faz o papel do primeiro atraso.
	var nextAttempt *time.Time
	if tx.Status() == domain.StatusPendingReference {
		t := tx.UpdatedAt()
		nextAttempt = &t
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, failure_code, result_balance,
			provider_id, external_transaction_id, idempotency_key, request_hash, round_id, game_id,
			reference_external_transaction_id, resolved_reference_id, created_at, updated_at, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		tx.ID().String(), tx.WalletID().String(), tx.PlayerID().String(), string(tx.Kind()), tx.Money().Minor(), tx.Money().Currency(),
		string(tx.Status()), nullable(string(tx.FailureCode())), resultBalance,
		nullable(tx.ProviderID()), nullable(tx.ExternalID()), nullable(tx.IdempotencyKey()), nullable(tx.RequestHash()),
		nullable(tx.RoundID()), nullable(tx.GameID()),
		nullable(tx.ReferenceExternalID()), nullable(tx.ResolvedReferenceID().String()), tx.CreatedAt(), tx.UpdatedAt(), nextAttempt)
	return err
}

func (r transactionRepo) GetByID(ctx context.Context, id domain.ID) (*domain.WagerTransaction, error) {
	return r.load(ctx, `SELECT `+transactionColumns+` FROM wager_transactions WHERE id = $1`, id.String())
}

func (r transactionRepo) GetByExternal(ctx context.Context, providerID, externalID string) (*domain.WagerTransaction, error) {
	return r.load(ctx, `SELECT `+transactionColumns+` FROM wager_transactions WHERE provider_id = $1 AND external_transaction_id = $2`,
		providerID, externalID)
}

func (r transactionRepo) HasProcessedReversal(ctx context.Context, referenceID domain.ID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM wager_transactions
		               WHERE resolved_reference_id = $1 AND kind IN ('REFUND', 'ROLLBACK') AND status = 'PROCESSED')`,
		referenceID.String()).Scan(&exists)
	return exists, err
}

func (r transactionRepo) UpdateOutcome(ctx context.Context, tx *domain.WagerTransaction) error {
	if tx.Status() == domain.StatusPending || tx.Status() == domain.StatusPendingReference {
		return fmt.Errorf("UpdateOutcome exige um desfecho, recebeu %s", tx.Status())
	}
	var resultBalance *int64
	if b, ok := tx.ResultBalance(); ok {
		v := b.Minor()
		resultBalance = &v
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE wager_transactions
		SET status = $2, failure_code = $3, result_balance = $4, resolved_reference_id = $5,
		    updated_at = $6, next_attempt_at = NULL
		WHERE id = $1 AND status = 'PENDING_REFERENCE'`,
		tx.ID().String(), string(tx.Status()), nullable(string(tx.FailureCode())), resultBalance,
		nullable(tx.ResolvedReferenceID().String()), tx.UpdatedAt())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return app.ErrStaleTransaction
	}
	return nil
}

func (r transactionRepo) load(ctx context.Context, sql string, args ...any) (*domain.WagerTransaction, error) {
	var (
		id, walletID, playerID, kind, currency, status                  string
		amount                                                          int64
		failureCode, provider, external, key, hash, round, game, refExt *string
		resultBalance                                                   *int64
		resolvedRef                                                     *string
		createdAt, updatedAt                                            time.Time
	)
	err := r.db.QueryRow(ctx, sql, args...).Scan(&id, &walletID, &playerID, &kind, &amount, &currency, &status, &failureCode,
		&resultBalance, &provider, &external, &key, &hash, &round, &game, &refExt, &resolvedRef, &createdAt, &updatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	money, err := domain.NewMoneyFromMinor(amount, currency)
	if err != nil {
		return nil, err
	}
	snap := domain.TransactionSnapshot{
		ID: domain.ID(id), WalletID: domain.ID(walletID), PlayerID: domain.ID(playerID),
		Kind: domain.Kind(kind), Money: money, Status: domain.Status(status),
		ProviderID: str(provider), ExternalID: str(external), IdempotencyKey: str(key), RequestHash: str(hash),
		RoundID: str(round), GameID: str(game), ReferenceExternalID: str(refExt),
		ResolvedReferenceID: domain.ID(str(resolvedRef)), FailureCode: domain.FailureCode(str(failureCode)),
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if resultBalance != nil {
		if snap.ResultBalance, err = domain.NewMoneyFromMinor(*resultBalance, currency); err != nil {
			return nil, err
		}
	}
	return domain.RehydrateTransaction(snap)
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
