//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"desafio/internal/app"
	"desafio/internal/domain"
)

func TestWalletRepoRoundTrip(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.seedWallet(t, "1000.00")

	_ = env.store.Read(ctx, func(ctx context.Context, r app.Repos) error {
		got, err := r.Wallets().Get(ctx, w.ID())
		if err != nil {
			t.Fatal(err)
		}
		if got.ID() != w.ID() || got.PlayerID() != w.PlayerID() || got.Balance().Amount() != "1000.00" ||
			got.Version() != 1 || !got.CreatedAt().Equal(w.CreatedAt()) || !got.UpdatedAt().Equal(w.UpdatedAt()) {
			t.Errorf("ida e volta divergente: %+v vs %+v", got, w)
		}
		if _, err := r.Wallets().Get(ctx, env.ids.NewID()); !errors.Is(err, app.ErrNotFound) {
			t.Errorf("inexistente: %v", err)
		}
		return nil
	})
}

func TestWalletRepoUpdateWithVersion(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.seedWallet(t, "100.00")

	err := env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		loaded, err := r.Wallets().GetForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		if _, err := loaded.Credit(env.ids.NewID(), env.ids.NewID(), env.money(t, "5.00"), env.clock.Now()); err != nil {
			return err
		}
		if err := r.Wallets().Update(ctx, loaded, 1); err != nil {
			return err
		}
		// a versão esperada (1) já não é a persistida (2): atualização desatualizada é rejeitada
		if err := r.Wallets().Update(ctx, loaded, 1); !errors.Is(err, app.ErrStaleWallet) {
			t.Errorf("atualização desatualizada: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = env.store.Read(ctx, func(ctx context.Context, r app.Repos) error {
		got, _ := r.Wallets().Get(ctx, w.ID())
		if got.Balance().Amount() != "105.00" || got.Version() != 2 {
			t.Errorf("saldo/versão = %s v%d", got.Balance(), got.Version())
		}
		return nil
	})
}

func TestTransactionRepoRoundTripAllStates(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.seedWallet(t, "100.00")

	processed := env.newExternalTx(t, w, domain.KindBet, "10.00")
	env.persistApplied(t, w, processed, domain.Reference{})
	rejected := env.newExternalTx(t, w, domain.KindBet, "999.00")
	env.persistApplied(t, w, rejected, domain.Reference{})
	pending := env.newExternalTx(t, w, domain.KindRefund, "10.00")
	env.persistApplied(t, w, pending, domain.Reference{}) // referência ausente → PENDING_REFERENCE
	loss := env.newExternalTx(t, w, domain.KindLoss, "0.00")
	env.persistApplied(t, w, loss, domain.Reference{})

	want := map[*domain.WagerTransaction]domain.Status{
		processed: domain.StatusProcessed, rejected: domain.StatusRejected,
		pending: domain.StatusPendingReference, loss: domain.StatusProcessed,
	}
	_ = env.store.Read(ctx, func(ctx context.Context, r app.Repos) error {
		for tx, status := range want {
			got, err := r.Transactions().GetByID(ctx, tx.ID())
			if err != nil {
				t.Fatalf("%s: %v", tx.ID(), err)
			}
			if got.Status() != status || got.Kind() != tx.Kind() || got.FailureCode() != tx.FailureCode() ||
				got.Money().Amount() != tx.Money().Amount() || got.ExternalID() != tx.ExternalID() ||
				got.IdempotencyKey() != tx.IdempotencyKey() || got.RequestHash() != tx.RequestHash() ||
				got.ReferenceExternalID() != tx.ReferenceExternalID() || !got.UpdatedAt().Equal(tx.UpdatedAt()) {
				t.Errorf("%s divergente: %+v vs %+v", status, got, tx)
			}
			gb, gok := got.ResultBalance()
			wb, wok := tx.ResultBalance()
			if gok != wok || (gok && !gb.Equal(wb)) {
				t.Errorf("%s: saldo observado %v/%v vs %v/%v", status, gb, gok, wb, wok)
			}
			byExt, err := r.Transactions().GetByExternal(ctx, tx.ProviderID(), tx.ExternalID())
			if err != nil || byExt.ID() != tx.ID() {
				t.Errorf("GetByExternal: %v", err)
			}
		}
		if _, err := r.Transactions().GetByID(ctx, env.ids.NewID()); !errors.Is(err, app.ErrNotFound) {
			t.Errorf("GetByID inexistente: %v", err)
		}
		if _, err := r.Transactions().GetByExternal(ctx, "prov", "nao-existe"); !errors.Is(err, app.ErrNotFound) {
			t.Errorf("GetByExternal inexistente: %v", err)
		}
		return nil
	})
}

func TestTransactionRepoOpeningRoundTrip(t *testing.T) {
	env := newEnv(t)
	w := env.seedWallet(t, "1000.00")
	var opening *domain.WagerTransaction
	_ = env.store.Read(context.Background(), func(ctx context.Context, r app.Repos) error {
		rows, err := r.Ledger().List(ctx, w.ID(), 0, 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("ledger: %v, %d linhas", err, len(rows))
		}
		opening, err = r.Transactions().GetByID(ctx, rows[0].Entry.TransactionID())
		if err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if opening.Kind() != domain.KindOpening || opening.Status() != domain.StatusProcessed || opening.ProviderID() != "" || opening.IdempotencyKey() != "" {
		t.Fatalf("OPENING: %+v", opening)
	}
	if b, ok := opening.ResultBalance(); !ok || b.Amount() != "1000.00" {
		t.Fatalf("saldo observado: %v, %v", b, ok)
	}
}

func TestTransactionRepoHasProcessedReversal(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.seedWallet(t, "100.00")
	bet := env.newExternalTx(t, w, domain.KindBet, "10.00")
	env.persistApplied(t, w, bet, domain.Reference{})
	refund := env.newExternalTx(t, w, domain.KindRefund, "10.00")
	env.persistApplied(t, w, refund, domain.Reference{Tx: bet})
	other := env.newExternalTx(t, w, domain.KindBet, "10.00")
	env.persistApplied(t, w, other, domain.Reference{})

	_ = env.store.Read(ctx, func(ctx context.Context, r app.Repos) error {
		if ok, err := r.Transactions().HasProcessedReversal(ctx, bet.ID()); err != nil || !ok {
			t.Errorf("bet reembolsada: %v, %v", ok, err)
		}
		if ok, err := r.Transactions().HasProcessedReversal(ctx, other.ID()); err != nil || ok {
			t.Errorf("outra aposta: %v, %v", ok, err)
		}
		got, _ := r.Transactions().GetByID(ctx, refund.ID())
		if got.ResolvedReferenceID() != bet.ID() {
			t.Errorf("referência resolvida = %s", got.ResolvedReferenceID())
		}
		return nil
	})
}

func TestLedgerRepoPaginationAndTotals(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.seedWallet(t, "100.00") // lançamento 1 (abertura)
	for range 3 {
		env.persistApplied(t, w, env.newExternalTx(t, w, domain.KindWin, "10.00"), domain.Reference{}) // 2, 3, 4
	}
	env.persistApplied(t, w, env.newExternalTx(t, w, domain.KindBet, "5.00"), domain.Reference{}) // 5

	_ = env.store.Read(ctx, func(ctx context.Context, r app.Repos) error {
		var seen []string
		var after int64
		pages := 0
		for {
			rows, err := r.Ledger().List(ctx, w.ID(), after, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			pages++
			for _, row := range rows {
				if row.Seq <= after {
					t.Errorf("seq %d não é maior que %d", row.Seq, after)
				}
				after = row.Seq
				seen = append(seen, row.Entry.ID().String())
			}
		}
		if pages != 3 || len(seen) != 5 {
			t.Errorf("%d páginas, %d lançamentos (esperado 3 e 5)", pages, len(seen))
		}
		uniq := map[string]bool{}
		for _, id := range seen {
			uniq[id] = true
		}
		if len(uniq) != 5 {
			t.Errorf("lançamentos repetidos: %v", seen)
		}

		tot, err := r.Ledger().Totals(ctx, w.ID(), "BRL")
		if err != nil {
			t.Fatal(err)
		}
		if tot.Count != 5 || tot.Credits.Amount() != "130.00" || tot.Debits.Amount() != "5.00" {
			t.Errorf("totais: %d, +%s, -%s", tot.Count, tot.Credits, tot.Debits)
		}
		empty, _ := r.Ledger().List(ctx, env.ids.NewID(), 0, 10)
		if len(empty) != 0 {
			t.Errorf("carteira sem lançamentos: %d", len(empty))
		}
		return nil
	})
}
