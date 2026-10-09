package bootstrap

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	httpadapter "desafio/internal/adapters/http"
	"desafio/internal/adapters/postgres"
	"desafio/internal/platform/config"
)

var databaseModule = fx.Module("database",
	fx.Provide(
		newPool,
		fx.Annotate(newPostgresChecker, fx.ResultTags(checkersGroup)),
	),
)

// newPool cria o pool e registra o ciclo de vida: o início valida o PostgreSQL e a parada fecha as
// conexões. Como outros componentes dependem do pool, ele é iniciado primeiro e fechado por último.
func newPool(lc fx.Lifecycle, cfg config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, cfg.CheckTimeout)
			defer cancel()
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("postgres: %w", err)
			}
			return nil
		},
		OnStop: func(context.Context) error {
			pool.Close()
			return nil
		},
	})
	return pool, nil
}

func newPostgresChecker(pool *pgxpool.Pool) httpadapter.Checker { return postgres.Checker{Pool: pool} }
