package postgres

import (
	"context"
	"time"

	"desafio/internal/app"
	"desafio/internal/domain"
)

type ledgerRepo struct{ db dbtx }

func (r ledgerRepo) Insert(ctx context.Context, e *domain.WalletLedgerEntry) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction, amount, balance_before, balance_after, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ID().String(), e.WalletID().String(), e.TransactionID().String(), string(e.Direction()),
		e.Money().Minor(), e.BalanceBefore().Minor(), e.BalanceAfter().Minor(), e.CreatedAt())
	return err
}

func (r ledgerRepo) List(ctx context.Context, walletID domain.ID, afterSeq int64, limit int) ([]app.LedgerRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT e.seq, e.id::text, e.wallet_id::text, e.transaction_id::text, e.direction,
		       e.amount, e.balance_before, e.balance_after, e.created_at, w.currency
		FROM wallet_ledger_entries e JOIN wallets w ON w.id = e.wallet_id
		WHERE e.wallet_id = $1 AND e.seq > $2
		ORDER BY e.seq
		LIMIT $3`, walletID.String(), afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []app.LedgerRow
	for rows.Next() {
		var (
			seq                      int64
			id, wid, txid, direction string
			amount, before, after    int64
			createdAt                time.Time
			currency                 string
		)
		if err := rows.Scan(&seq, &id, &wid, &txid, &direction, &amount, &before, &after, &createdAt, &currency); err != nil {
			return nil, err
		}
		m, err := domain.NewMoneyFromMinor(amount, currency)
		if err != nil {
			return nil, err
		}
		b, _ := domain.NewMoneyFromMinor(before, currency)
		a, _ := domain.NewMoneyFromMinor(after, currency)
		entry, err := domain.RehydrateLedgerEntry(domain.ID(id), domain.ID(wid), domain.ID(txid), domain.Direction(direction), m, b, a, createdAt)
		if err != nil {
			return nil, err
		}
		out = append(out, app.LedgerRow{Seq: seq, Entry: entry})
	}
	return out, rows.Err()
}

func (r ledgerRepo) Totals(ctx context.Context, walletID domain.ID, currency string) (app.LedgerTotals, error) {
	var credits, debits int64
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount) FILTER (WHERE direction = 'CREDIT'), 0)::bigint,
		       COALESCE(SUM(amount) FILTER (WHERE direction = 'DEBIT'), 0)::bigint,
		       count(*)
		FROM wallet_ledger_entries WHERE wallet_id = $1`, walletID.String()).Scan(&credits, &debits, &count)
	if err != nil {
		return app.LedgerTotals{}, err
	}
	c, err := domain.NewMoneyFromMinor(credits, currency)
	if err != nil {
		return app.LedgerTotals{}, err
	}
	d, err := domain.NewMoneyFromMinor(debits, currency)
	if err != nil {
		return app.LedgerTotals{}, err
	}
	return app.LedgerTotals{Credits: c, Debits: d, Count: count}, nil
}
