//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
	"desafio/internal/domain"
	"desafio/internal/platform/clock"
	"desafio/internal/platform/ids"
)

// testEnv reúne um Store com pool próprio, o gerador de ids e o relógio.
type testEnv struct {
	store *postgres.Store
	ids   ids.Generator
	clock clock.System
}

func newEnv(t *testing.T) testEnv {
	t.Helper()
	return testEnv{store: postgres.NewStore(newPool(t))}
}

func (e testEnv) money(t *testing.T, amount string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// seedWallet abre (e persiste) uma carteira com o saldo informado, junto da OPENING e do lançamento.
func (e testEnv) seedWallet(t *testing.T, balance string) *domain.Wallet {
	t.Helper()
	o, err := domain.OpenWallet(e.ids.NewID(), e.ids.NewID(), e.ids.NewID(), e.ids.NewID(), e.money(t, balance), e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	err = e.store.InTx(context.Background(), func(ctx context.Context, r app.Repos) error {
		if err := r.Wallets().Insert(ctx, o.Wallet); err != nil {
			return err
		}
		if o.Transaction == nil {
			return nil
		}
		if err := r.Transactions().Insert(ctx, o.Transaction); err != nil {
			return err
		}
		return r.Ledger().Insert(ctx, o.Entry)
	})
	if err != nil {
		t.Fatal(err)
	}
	return o.Wallet
}

// newExternalTx cria (sem persistir) uma operação externa com ids e chaves únicas.
func (e testEnv) newExternalTx(t *testing.T, w *domain.Wallet, kind domain.Kind, amount string, mutate ...func(*domain.ExternalParams)) *domain.WagerTransaction {
	t.Helper()
	ext := string(e.ids.NewID())
	p := domain.ExternalParams{
		ID: e.ids.NewID(), ProviderID: "prov", ExternalID: ext, IdempotencyKey: "k-" + ext, RequestHash: "h",
		WalletID: w.ID(), PlayerID: w.PlayerID(), RoundID: "round", GameID: "game", Kind: kind,
		Money: e.money(t, amount), Now: e.clock.Now(),
	}
	if kind == domain.KindRefund || kind == domain.KindRollback {
		p.ReferenceExternalID = "ref"
	}
	for _, m := range mutate {
		m(&p)
	}
	tx, err := domain.NewExternalTransaction(p)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// persistApplied aplica a operação ao domínio e grava carteira, transação e lançamento (como um caso de uso faria).
func (e testEnv) persistApplied(t *testing.T, w *domain.Wallet, tx *domain.WagerTransaction, ref domain.Reference) *domain.WalletLedgerEntry {
	t.Helper()
	before := w.Version()
	entry, err := domain.Apply(w, tx, ref, e.ids.NewID(), e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	err = e.store.InTx(context.Background(), func(ctx context.Context, r app.Repos) error {
		if entry != nil {
			if err := r.Wallets().Update(ctx, w, before); err != nil {
				return err
			}
		}
		if err := r.Transactions().Insert(ctx, tx); err != nil {
			return err
		}
		if entry != nil {
			return r.Ledger().Insert(ctx, entry)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func (e testEnv) parseID(s string) (domain.ID, error) { return domain.ParseID(s) }
