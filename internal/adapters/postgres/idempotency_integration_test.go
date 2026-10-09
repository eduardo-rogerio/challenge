//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
)

const testLease = 30 * time.Second

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL não definida")
	}
	pool, err := postgres.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// freshStore limpa a tabela e devolve um store com a lease pedida.
func freshStore(t *testing.T, lease time.Duration) (*postgres.IdempotencyStore, *pgxpool.Pool) {
	t.Helper()
	pool := newPool(t)
	if _, err := pool.Exec(context.Background(), `TRUNCATE idempotency_keys`); err != nil {
		t.Fatalf("truncate (migrations aplicadas?): %v", err)
	}
	return postgres.NewIdempotencyStore(pool, lease), pool
}

func result() app.TransactionResult {
	bal := app.Money{Amount: "975.00", Currency: "BRL"}
	return app.TransactionResult{TransactionID: "tx-1", Status: app.StatusProcessed, Balance: &bal}
}

func TestIdempotencyBeginCompleteReplay(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t, testLease)

	if res, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1"); res != nil || err != nil {
		t.Fatalf("primeira chamada: %v, %v", res, err)
	}
	if _, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1"); !errors.Is(err, app.ErrInProgress) {
		t.Fatalf("em andamento: %v", err)
	}
	if err := s.Complete(ctx, "k1", result()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1")
	if err != nil || got == nil {
		t.Fatalf("replay: %v, %v", got, err)
	}
	if got.TransactionID != "tx-1" || got.Status != app.StatusProcessed || got.Balance == nil || got.Balance.Amount != "975.00" {
		t.Fatalf("resultado inesperado: %+v", got)
	}
}

func TestIdempotencyConflicts(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t, testLease)
	if _, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Begin(ctx, "k1", "prov", "ext-1", "OUTRO"); !errors.Is(err, app.ErrIdempotencyConflict) {
		t.Errorf("mesma chave, hash diferente: %v", err)
	}
	if _, err := s.Begin(ctx, "k2", "prov", "ext-1", "h1"); !errors.Is(err, app.ErrIdempotencyConflict) {
		t.Errorf("mesma operação externa, outra chave: %v", err)
	}
	// Outro provedor com o mesmo id externo é outra operação.
	if res, err := s.Begin(ctx, "k3", "outro-prov", "ext-1", "h1"); res != nil || err != nil {
		t.Errorf("outro provedor: %v, %v", res, err)
	}
}

func TestIdempotencyReleaseAllowsReuse(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t, testLease)
	if _, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if res, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1"); res != nil || err != nil {
		t.Fatalf("reuso após Release: %v, %v", res, err)
	}
}

func TestIdempotencyExpiredLeaseIsTakenOver(t *testing.T) {
	ctx := context.Background()
	s, pool := freshStore(t, 300*time.Millisecond)
	other := postgres.NewIdempotencyStore(newPool(t), 300*time.Millisecond)
	_ = pool

	if _, err := s.Begin(ctx, "k1", "prov", "ext-1", "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Begin(ctx, "k1", "prov", "ext-1", "h1"); !errors.Is(err, app.ErrInProgress) {
		t.Fatalf("dentro da lease: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if res, err := other.Begin(ctx, "k1", "prov", "ext-1", "h1"); res != nil || err != nil {
		t.Fatalf("retomada após a lease: %v, %v", res, err)
	}
	if err := other.Complete(ctx, "k1", result()); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotencyConcurrentBurstAcrossPools(t *testing.T) {
	ctx := context.Background()
	s0, _ := freshStore(t, testLease)
	stores := []*postgres.IdempotencyStore{s0, postgres.NewIdempotencyStore(newPool(t), testLease), postgres.NewIdempotencyStore(newPool(t), testLease)}

	var (
		wg         sync.WaitGroup
		started    atomic.Int32
		inProgress atomic.Int32
		winner     atomic.Pointer[postgres.IdempotencyStore]
	)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := stores[i%len(stores)]
			res, err := s.Begin(ctx, "burst", "prov", "ext-burst", "h")
			switch {
			case errors.Is(err, app.ErrInProgress):
				inProgress.Add(1)
			case err != nil:
				t.Errorf("erro inesperado: %v", err)
			case res == nil:
				started.Add(1)
				winner.Store(s)
			}
		}()
	}
	wg.Wait()
	if started.Load() != 1 || inProgress.Load() != 49 {
		t.Fatalf("started=%d inProgress=%d (esperado 1 e 49)", started.Load(), inProgress.Load())
	}

	if err := winner.Load().Complete(ctx, "burst", result()); err != nil {
		t.Fatal(err)
	}
	// Um pool novo (simulando outro processo/reinício) enxerga o resultado persistido.
	fresh := postgres.NewIdempotencyStore(newPool(t), testLease)
	got, err := fresh.Begin(ctx, "burst", "prov", "ext-burst", "h")
	if err != nil || got == nil || got.TransactionID != "tx-1" {
		t.Fatalf("replay com pool novo: %+v, %v", got, err)
	}
}

func TestIdempotencySchemaRejectsInconsistentRows(t *testing.T) {
	_, pool := freshStore(t, testLease)
	ctx := context.Background()
	// COMPLETED sem resposta
	_, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (key, provider_id, external_transaction_id, request_hash, state, locked_until)
		VALUES ('x','p','e','h','COMPLETED', now())`)
	if err == nil {
		t.Error("COMPLETED sem response deveria violar o CHECK")
	}
	// IN_PROGRESS com resposta
	_, err = pool.Exec(ctx, `INSERT INTO idempotency_keys (key, provider_id, external_transaction_id, request_hash, state, response, locked_until)
		VALUES ('y','p','e2','h','IN_PROGRESS','{}', now())`)
	if err == nil {
		t.Error("IN_PROGRESS com response deveria violar o CHECK")
	}
	// Estado inválido
	_, err = pool.Exec(ctx, `INSERT INTO idempotency_keys (key, provider_id, external_transaction_id, request_hash, state, locked_until)
		VALUES ('z','p','e3','h','BOGUS', now())`)
	if err == nil {
		t.Error("estado inválido deveria violar o CHECK")
	}
}
