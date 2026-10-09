//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
	"desafio/internal/domain"
)

func TestStoreInTxCommitsAndRollsBack(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	mk := func() *domain.Wallet {
		w, err := domain.NewWallet(env.ids.NewID(), env.ids.NewID(), env.money(t, "1.00"), env.clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		return w
	}

	committed, rolledBack := mk(), mk()
	if err := env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		return r.Wallets().Insert(ctx, committed)
	}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		if err := r.Wallets().Insert(ctx, rolledBack); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("erro %v", err)
	}

	_ = env.store.Read(ctx, func(ctx context.Context, r app.Repos) error {
		if _, err := r.Wallets().Get(ctx, committed.ID()); err != nil {
			t.Errorf("carteira confirmada deveria existir: %v", err)
		}
		if _, err := r.Wallets().Get(ctx, rolledBack.ID()); !errors.Is(err, app.ErrNotFound) {
			t.Errorf("carteira descartada deveria não existir: %v", err)
		}
		return nil
	})
}

func TestStoreReadIsReadOnly(t *testing.T) {
	env := newEnv(t)
	w, _ := domain.NewWallet(env.ids.NewID(), env.ids.NewID(), env.money(t, "1.00"), env.clock.Now())
	err := env.store.Read(context.Background(), func(ctx context.Context, r app.Repos) error {
		return r.Wallets().Insert(ctx, w)
	})
	if err == nil {
		t.Fatal("escrever dentro de Read deveria falhar")
	}
}

func TestStoreLockTimeoutIsUnavailable(t *testing.T) {
	holder := newEnv(t)
	w := holder.seedWallet(t, "100.00")
	waiter := postgres.NewStore(newPool(t)).WithLockTimeout(200 * time.Millisecond)
	ctx := context.Background()

	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- holder.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
			if _, err := r.Wallets().GetForUpdate(ctx, w.ID()); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	start := time.Now()
	err := waiter.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		_, err := r.Wallets().GetForUpdate(ctx, w.ID())
		return err
	})
	if !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("esperava ErrUnavailable, obtido %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("a espera pelo lock demorou %v", time.Since(start))
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// liberado o lock, a mesma operação passa
	if err := waiter.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		_, err := r.Wallets().GetForUpdate(ctx, w.ID())
		return err
	}); err != nil {
		t.Fatalf("depois de liberar o lock: %v", err)
	}
}

func TestStoreMapsUniqueWalletToConflict(t *testing.T) {
	env := newEnv(t)
	first := env.seedWallet(t, "1.00")
	dup, _ := domain.NewWallet(env.ids.NewID(), first.PlayerID(), env.money(t, "2.00"), env.clock.Now())
	err := env.store.InTx(context.Background(), func(ctx context.Context, r app.Repos) error {
		return r.Wallets().Insert(ctx, dup)
	})
	if !errors.Is(err, app.ErrConflict) {
		t.Fatalf("esperava ErrConflict, obtido %v", err)
	}
}

// Um banco inacessível (conexão recusada) deve ser indisponibilidade temporária (503), em todos os pontos de acesso.
func TestUnreachableDatabaseIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := postgres.New(ctx, "postgres://app:app@127.0.0.1:1/app?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	store := postgres.NewStore(pool)
	if err := store.InTx(ctx, func(context.Context, app.Repos) error { return nil }); !errors.Is(err, app.ErrUnavailable) {
		t.Errorf("InTx: %v", err)
	}
	if err := store.Read(ctx, func(context.Context, app.Repos) error { return nil }); !errors.Is(err, app.ErrUnavailable) {
		t.Errorf("Read: %v", err)
	}
	idem := postgres.NewIdempotencyStore(pool, time.Second)
	if _, err := idem.Begin(ctx, "k", "p", "e", "h"); !errors.Is(err, app.ErrUnavailable) {
		t.Errorf("Begin: %v", err)
	}
	if err := idem.Complete(ctx, "k", app.TransactionResult{}); !errors.Is(err, app.ErrUnavailable) {
		t.Errorf("Complete: %v", err)
	}
	if err := idem.Release(ctx, "k"); !errors.Is(err, app.ErrUnavailable) {
		t.Errorf("Release: %v", err)
	}
}
