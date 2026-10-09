package postgres

import (
	"context"
	"time"

	"desafio/internal/app"
	"desafio/internal/domain"
)

type walletRepo struct{ db dbtx }

const walletColumns = `id::text, player_id::text, currency, balance, version, created_at, updated_at`

func (r walletRepo) Insert(ctx context.Context, w *domain.Wallet) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		w.ID().String(), w.PlayerID().String(), w.Currency(), w.Balance().Minor(), w.Version(), w.CreatedAt(), w.UpdatedAt())
	return err
}

func (r walletRepo) Get(ctx context.Context, id domain.ID) (*domain.Wallet, error) {
	return r.load(ctx, `SELECT `+walletColumns+` FROM wallets WHERE id = $1`, id)
}

func (r walletRepo) GetForUpdate(ctx context.Context, id domain.ID) (*domain.Wallet, error) {
	return r.load(ctx, `SELECT `+walletColumns+` FROM wallets WHERE id = $1 FOR UPDATE`, id)
}

func (r walletRepo) load(ctx context.Context, sql string, id domain.ID) (*domain.Wallet, error) {
	var (
		wid, player, currency string
		balance, version      int64
		createdAt, updatedAt  time.Time
	)
	if err := r.db.QueryRow(ctx, sql, id.String()).Scan(&wid, &player, &currency, &balance, &version, &createdAt, &updatedAt); err != nil {
		return nil, notFound(err)
	}
	money, err := domain.NewMoneyFromMinor(balance, currency)
	if err != nil {
		return nil, err
	}
	return domain.RehydrateWallet(domain.ID(wid), domain.ID(player), money, version, createdAt, updatedAt)
}

func (r walletRepo) Update(ctx context.Context, w *domain.Wallet, expectedVersion int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE wallets SET balance = $2, version = $3, updated_at = $4
		WHERE id = $1 AND version = $5`,
		w.ID().String(), w.Balance().Minor(), w.Version(), w.UpdatedAt(), expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return app.ErrStaleWallet
	}
	return nil
}
