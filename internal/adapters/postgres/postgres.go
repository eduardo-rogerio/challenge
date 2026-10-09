// Package postgres cria o pool de conexões e o health check do PostgreSQL.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New cria o pool; a conexão é estabelecida sob demanda (use Check para validar).
func New(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, url)
}

type Checker struct{ Pool *pgxpool.Pool }

func (Checker) Name() string                      { return "postgres" }
func (c Checker) Check(ctx context.Context) error { return c.Pool.Ping(ctx) }
