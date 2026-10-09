package postgres

import (
	"context"
	"time"

	"desafio/internal/app"
	"desafio/internal/domain"
)

type pendingReferenceRepo struct{ db dbtx }

// Claim seleciona as pendências vencidas com SKIP LOCKED e já adia o agendamento delas (lease) na mesma
// instrução; as linhas ficam livres de lock ao fim da transação, mas só voltam a ser vencidas depois de `until`.
func (r pendingReferenceRepo) Claim(ctx context.Context, now, until time.Time, limit int) ([]app.PendingReference, error) {
	rows, err := r.db.Query(ctx, `
		WITH due AS (
			SELECT id FROM wager_transactions
			WHERE status = 'PENDING_REFERENCE' AND next_attempt_at <= $1
			ORDER BY next_attempt_at, id
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE wager_transactions t SET next_attempt_at = $2
		FROM due WHERE t.id = due.id
		RETURNING t.id::text, t.wallet_id::text, t.reference_attempts`, now, until, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []app.PendingReference
	for rows.Next() {
		var id, wallet string
		var attempts int
		if err := rows.Scan(&id, &wallet, &attempts); err != nil {
			return nil, err
		}
		out = append(out, app.PendingReference{ID: domain.ID(id), WalletID: domain.ID(wallet), Attempts: attempts})
	}
	return out, rows.Err()
}

func (r pendingReferenceRepo) Reschedule(ctx context.Context, id domain.ID, attempts int, next time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE wager_transactions SET reference_attempts = $2, next_attempt_at = $3
		WHERE id = $1 AND status = 'PENDING_REFERENCE'`, id.String(), attempts, next)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return app.ErrStaleTransaction
	}
	return nil
}
