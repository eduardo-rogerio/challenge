package bootstrap

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
	"desafio/internal/platform/clock"
	"desafio/internal/platform/config"
	"desafio/internal/platform/ids"
	"desafio/internal/platform/metrics"
)

var observabilityModule = fx.Module("observability",
	fx.Provide(metrics.New),
)

var applicationModule = fx.Module("application",
	fx.Provide(
		newStore,
		newIdempotencyStore,
		func() app.IDGenerator { return ids.Generator{} },
		func() app.Clock { return clock.System{} },
		newWallets,
		newTransactions,
	),
)

func newStore(pool *pgxpool.Pool) app.Store { return postgres.NewStore(pool) }

func newIdempotencyStore(pool *pgxpool.Pool, cfg config.Config) app.IdempotencyStore {
	return postgres.NewIdempotencyStore(pool, cfg.IdempotencyLease)
}

func newWallets(store app.Store, ids app.IDGenerator, clk app.Clock, log *slog.Logger, m *metrics.Metrics) app.Wallets {
	return app.NewWalletService(store, ids, clk,
		app.WithLogger(log), app.WithDivergenceHook(func(string) { m.ReconciliationDivergence() }))
}

// newTransactions devolve o serviço já envolvido pelo decorador de idempotência: o serviço sem
// decorador nunca entra no grafo, então nenhum consumidor pode contorná-lo por engano.
func newTransactions(store app.Store, ids app.IDGenerator, clk app.Clock, idem app.IdempotencyStore) app.Transactions {
	return app.NewIdempotentTransactions(idem, app.NewTransactionService(store, ids, clk))
}
