//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"desafio/internal/adapters/postgres"
)

func TestPoolPing(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL não definida")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := postgres.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := (postgres.Checker{Pool: pool}).Check(ctx); err != nil {
		t.Fatal(err)
	}
}
