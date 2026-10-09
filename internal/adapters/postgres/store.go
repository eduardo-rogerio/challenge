package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"desafio/internal/app"
)

// DefaultLockTimeout limita a espera por locks de carteira dentro de uma transação.
const DefaultLockTimeout = 5 * time.Second

// Store implementa app.Store sobre um pool pgx.
type Store struct {
	pool        *pgxpool.Pool
	lockTimeout time.Duration
}

var _ app.Store = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, lockTimeout: DefaultLockTimeout}
}

// WithLockTimeout devolve uma cópia com outro prazo de lock.
func (s *Store) WithLockTimeout(d time.Duration) *Store {
	c := *s
	c.lockTimeout = d
	return &c
}

// dbtx é o subconjunto de pgx.Tx usado pelos repositórios.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// InTx executa fn em uma transação READ COMMITTED; erro faz rollback, sucesso faz commit.
func (s *Store) InTx(ctx context.Context, fn func(context.Context, app.Repos) error) error {
	return s.run(ctx, pgx.TxOptions{}, fn)
}

// Read executa fn em uma visão consistente (REPEATABLE READ) somente leitura.
func (s *Store) Read(ctx context.Context, fn func(context.Context, app.Repos) error) error {
	return s.run(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

func (s *Store) run(ctx context.Context, opts pgx.TxOptions, fn func(context.Context, app.Repos) error) error {
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return mapError(err)
	}
	// O rollback não depende do cancelamento da requisição; é inofensivo após o commit.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", s.lockTimeout.Milliseconds())); err != nil {
		return mapError(err)
	}
	if err := fn(ctx, repos{db: tx}); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

type repos struct{ db dbtx }

func (r repos) Wallets() app.WalletRepository           { return walletRepo{r.db} }
func (r repos) Transactions() app.TransactionRepository { return transactionRepo{r.db} }
func (r repos) Ledger() app.LedgerRepository            { return ledgerRepo{r.db} }
func (r repos) PendingReferences() app.PendingReferenceRepository {
	return pendingReferenceRepo{r.db}
}

// notFound converte "nenhuma linha" em app.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}
