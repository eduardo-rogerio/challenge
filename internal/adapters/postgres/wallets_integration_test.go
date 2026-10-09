//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"desafio/internal/app"
	"desafio/internal/domain"
)

func (e testEnv) walletService(opts ...app.WalletOption) *app.WalletService {
	return app.NewWalletService(e.store, e.ids, e.clock, opts...)
}

func openInput(player, amount string) app.OpenWalletInput {
	return app.OpenWalletInput{PlayerID: player, InitialBalance: app.Money{Amount: amount, Currency: "BRL"}}
}

func countRows(t *testing.T, env testEnv, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := newPool(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenWalletWithPositiveBalance(t *testing.T) {
	env := newEnv(t)
	svc := env.walletService()
	player := string(env.ids.NewID())

	w, err := svc.Open(context.Background(), openInput(player, "1000.00"))
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance.Amount != "1000.00" || w.Version != 1 || w.PlayerID != player {
		t.Fatalf("carteira: %+v", w)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wager_transactions WHERE wallet_id = $1 AND kind = 'OPENING' AND status = 'PROCESSED' AND result_balance = 100000`, w.ID); n != 1 {
		t.Errorf("transações OPENING = %d", n)
	}
	page, err := svc.Ledger(context.Background(), w.ID, "", 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("ledger: %v, %+v", err, page)
	}
	e := page.Entries[0]
	if e.Direction != "CREDIT" || e.BalanceBefore.Amount != "0.00" || e.BalanceAfter.Amount != "1000.00" {
		t.Errorf("lançamento: %+v", e)
	}
}

func TestOpenWalletWithZeroBalanceWritesOnlyTheWallet(t *testing.T) {
	env := newEnv(t)
	w, err := env.walletService().Open(context.Background(), openInput(string(env.ids.NewID()), "0.00"))
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wager_transactions WHERE wallet_id = $1`, w.ID); n != 0 {
		t.Errorf("transações = %d", n)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, w.ID); n != 0 {
		t.Errorf("lançamentos = %d", n)
	}
}

func TestOpenWalletInvalidInput(t *testing.T) {
	env := newEnv(t)
	svc := env.walletService()
	for name, in := range map[string]app.OpenWalletInput{
		"jogador":  openInput("abc", "1.00"),
		"formato":  openInput(string(env.ids.NewID()), "10"),
		"negativo": openInput(string(env.ids.NewID()), "-1.00"),
	} {
		if _, err := svc.Open(context.Background(), in); !errors.Is(err, app.ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOpenWalletConcurrentSamePlayerAndCurrency(t *testing.T) {
	env := newEnv(t)
	player := string(env.ids.NewID())
	var created, conflicts atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// cada tentativa usa um serviço (e um pool) independente, como instâncias distintas
			svc := newEnv(t).walletService()
			switch _, err := svc.Open(context.Background(), openInput(player, "100.00")); {
			case err == nil:
				created.Add(1)
			case errors.Is(err, app.ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("erro inesperado: %v", err)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 || conflicts.Load() != 9 {
		t.Fatalf("criadas=%d conflitos=%d", created.Load(), conflicts.Load())
	}
	if n := countRows(t, env, `SELECT count(*) FROM wallets WHERE player_id = $1`, player); n != 1 {
		t.Errorf("carteiras = %d", n)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wager_transactions WHERE player_id = $1`, player); n != 1 {
		t.Errorf("transações = %d (registros órfãos?)", n)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wallet_ledger_entries e JOIN wallets w ON w.id = e.wallet_id WHERE w.player_id = $1`, player); n != 1 {
		t.Errorf("lançamentos = %d", n)
	}
}

func TestGetWallet(t *testing.T) {
	env := newEnv(t)
	svc := env.walletService()
	seeded := env.seedWallet(t, "250.00")

	got, err := svc.Get(context.Background(), seeded.ID().String())
	if err != nil || got.Balance.Amount != "250.00" || got.Version != 1 || got.ID != seeded.ID().String() {
		t.Fatalf("Get: %+v, %v", got, err)
	}
	if _, err := svc.Get(context.Background(), string(env.ids.NewID())); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("inexistente: %v", err)
	}
	if _, err := svc.Get(context.Background(), "abc"); !errors.Is(err, app.ErrInvalidInput) {
		t.Errorf("id inválido: %v", err)
	}
}

func TestLedgerPagination(t *testing.T) {
	env := newEnv(t)
	svc := env.walletService()
	ctx := context.Background()
	w := env.seedWallet(t, "100.00")
	for range 4 {
		env.persistApplied(t, w, env.newExternalTx(t, w, domain.KindWin, "1.00"), domain.Reference{})
	}

	var all []string
	cursor, pages := "", 0
	for {
		page, err := svc.Ledger(ctx, w.ID().String(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, e := range page.Entries {
			all = append(all, e.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if pages != 3 || len(all) != 5 {
		t.Fatalf("%d páginas, %d lançamentos (esperado 3 e 5)", pages, len(all))
	}
	uniq := map[string]bool{}
	for _, id := range all {
		uniq[id] = true
	}
	if len(uniq) != 5 {
		t.Errorf("repetição ou omissão: %v", all)
	}
	// a primeira entrada é a abertura (CREDIT de 100.00)
	first, _ := svc.Ledger(ctx, w.ID().String(), "", 1)
	if len(first.Entries) != 1 || first.Entries[0].Money.Amount != "100.00" || first.NextCursor == nil {
		t.Errorf("primeira página: %+v", first)
	}
}

func TestLedgerEdgeCases(t *testing.T) {
	env := newEnv(t)
	svc := env.walletService()
	ctx := context.Background()
	empty, err := svc.Open(ctx, openInput(string(env.ids.NewID()), "0.00"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.Ledger(ctx, empty.ID, "", 50)
	if err != nil || page.Entries == nil || len(page.Entries) != 0 || page.NextCursor != nil {
		t.Errorf("ledger vazio: %+v, %v", page, err)
	}
	if _, err := svc.Ledger(ctx, empty.ID, "cursor-invalido", 10); !errors.Is(err, app.ErrInvalidInput) {
		t.Errorf("cursor inválido: %v", err)
	}
	if _, err := svc.Ledger(ctx, empty.ID, "", 101); !errors.Is(err, app.ErrInvalidInput) {
		t.Errorf("limit 101: %v", err)
	}
	if _, err := svc.Ledger(ctx, string(env.ids.NewID()), "", 10); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("carteira inexistente: %v", err)
	}
}

func TestReconcileConsistentWallet(t *testing.T) {
	env := newEnv(t)
	svc := env.walletService()
	w := env.seedWallet(t, "1000.00")
	env.persistApplied(t, w, env.newExternalTx(t, w, domain.KindBet, "25.00"), domain.Reference{})

	rec, err := svc.Reconcile(context.Background(), w.ID().String())
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Consistent || rec.StoredBalance.Amount != "975.00" || rec.CalculatedBalance.Amount != "975.00" ||
		rec.Difference.Amount != "0.00" || rec.CheckedEntries != 2 || rec.WalletID != w.ID().String() {
		t.Fatalf("reconciliação: %+v", rec)
	}
}

func TestReconcileDetectsDivergenceWithoutFixingIt(t *testing.T) {
	env := newEnv(t)
	var hits []string
	svc := env.walletService(app.WithDivergenceHook(func(id string) { hits = append(hits, id) }))
	w := env.seedWallet(t, "100.00")
	// adulteração fora do fluxo normal: o saldo armazenado passa a diferir do ledger
	execOK(t, newPool(t), `UPDATE wallets SET balance = balance + 500 WHERE id = $1`, w.ID().String())

	rec, err := svc.Reconcile(context.Background(), w.ID().String())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Consistent || rec.StoredBalance.Amount != "105.00" || rec.CalculatedBalance.Amount != "100.00" || rec.Difference.Amount != "5.00" {
		t.Fatalf("divergência: %+v", rec)
	}
	if len(hits) != 1 || hits[0] != w.ID().String() {
		t.Errorf("hook de divergência: %v", hits)
	}
	// a reconciliação não corrige nada
	got, _ := svc.Get(context.Background(), w.ID().String())
	if got.Balance.Amount != "105.00" || got.Version != 1 {
		t.Errorf("a reconciliação alterou a carteira: %+v", got)
	}
}

func TestReconcileUnknownWallet(t *testing.T) {
	env := newEnv(t)
	if _, err := env.walletService().Reconcile(context.Background(), string(env.ids.NewID())); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
}
