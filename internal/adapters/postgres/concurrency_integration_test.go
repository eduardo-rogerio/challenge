//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
)

// threeInstances devolve três ambientes com pools de conexão independentes, simulando três instâncias.
func threeInstances(t *testing.T) [3]testEnv {
	t.Helper()
	return [3]testEnv{newEnv(t), newEnv(t), newEnv(t)}
}

// concurrently executa fn(i) para i em [0,n) começando todas ao mesmo tempo.
func concurrently(n int, fn func(i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			fn(i)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

// assertReconciled confere que o saldo armazenado é igual a créditos menos débitos do ledger.
func assertReconciled(t *testing.T, env testEnv, w app.Wallet) {
	t.Helper()
	rec, err := env.walletService().Reconcile(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Consistent {
		t.Fatalf("carteira %s inconsistente: %+v", w.ID, rec)
	}
}

// Cenário obrigatório do desafio: duas apostas distintas de 80.00 sobre 100.00 ao mesmo tempo.
func TestConcurrentTwoBetsOfEightyOnHundred(t *testing.T) {
	inst := threeInstances(t)
	for round := range 20 {
		w := inst[0].openWallet(t, "100.00")
		inputs := [2]app.SubmitInput{inst[0].submitIn(w, "BET", "80.00"), inst[0].submitIn(w, "BET", "80.00")}
		var results [2]app.TransactionResult
		concurrently(2, func(i int) {
			res, err := inst[(round+i)%3].txService().Submit(context.Background(), inputs[i])
			if err != nil {
				t.Errorf("rodada %d: %v", round, err)
			}
			results[i] = res
		})

		processed, rejected := 0, 0
		for _, r := range results {
			switch {
			case r.Status == "PROCESSED":
				processed++
			case r.Status == "REJECTED" && r.FailureCode == "INSUFFICIENT_FUNDS":
				rejected++
			}
		}
		if processed != 1 || rejected != 1 {
			t.Fatalf("rodada %d: processadas=%d rejeitadas=%d (%+v)", round, processed, rejected, results)
		}
		if bal, ver := inst[0].balance(t, w); bal != "20.00" || ver != 2 {
			t.Fatalf("rodada %d: saldo %s v%d", round, bal, ver)
		}
		if n := countRows(t, inst[0], `SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id = $1 AND direction = 'DEBIT'`, w.ID); n != 1 {
			t.Fatalf("rodada %d: débitos = %d", round, n)
		}
		assertReconciled(t, inst[0], w)
	}
}

// A mesma aposta enviada 50 vezes em paralelo, por três instâncias, resulta em um único débito.
func TestConcurrentSameBetFiftyTimes(t *testing.T) {
	inst := threeInstances(t)
	w := inst[0].openWallet(t, "1000.00")
	in := inst[0].submitIn(w, "BET", "25.00")

	var services [3]*app.IdempotentTransactions
	for i := range services {
		store := postgres.NewIdempotencyStore(newPool(t), 30*time.Second)
		services[i] = app.NewIdempotentTransactions(store, inst[i].txService())
	}

	var firsts, replays, inProgress atomic.Int32
	var mu sync.Mutex
	txIDs := map[string]bool{}
	concurrently(50, func(i int) {
		res, err := services[i%3].Submit(context.Background(), in)
		switch {
		case errors.Is(err, app.ErrInProgress):
			inProgress.Add(1)
		case err != nil:
			t.Errorf("erro inesperado: %v", err)
		default:
			if res.IdempotentReplay {
				replays.Add(1)
			} else {
				firsts.Add(1)
			}
			mu.Lock()
			txIDs[res.TransactionID] = true
			mu.Unlock()
		}
	})
	if firsts.Load() != 1 {
		t.Fatalf("execuções originais = %d (replays=%d, em andamento=%d)", firsts.Load(), replays.Load(), inProgress.Load())
	}
	if len(txIDs) != 1 {
		t.Fatalf("respostas descrevem %d transações diferentes", len(txIDs))
	}
	// quem recebeu "em andamento" reenvia e obtém o mesmo resultado
	for i := range 50 {
		res, err := services[i%3].Submit(context.Background(), in)
		if err != nil || !res.IdempotentReplay || !txIDs[res.TransactionID] || res.Balance.Amount != "975.00" {
			t.Fatalf("reenvio %d: %+v, %v", i, res, err)
		}
	}
	if n := countRows(t, inst[0], `SELECT count(*) FROM wager_transactions WHERE external_transaction_id = $1`, in.ExternalTransactionID); n != 1 {
		t.Fatalf("transações = %d", n)
	}
	if bal, ver := inst[0].balance(t, w); bal != "975.00" || ver != 2 {
		t.Fatalf("saldo %s v%d (um único débito esperado)", bal, ver)
	}
	assertReconciled(t, inst[0], w)
}

// Carteiras distintas avançam em paralelo e permanecem consistentes.
func TestConcurrentIndependentWallets(t *testing.T) {
	inst := threeInstances(t)
	const wallets, betsPerWallet = 20, 5
	ws := make([]app.Wallet, wallets)
	for i := range ws {
		ws[i] = inst[0].openWallet(t, "100.00")
	}
	concurrently(wallets*betsPerWallet, func(i int) {
		w := ws[i%wallets]
		if _, err := inst[i%3].txService().Submit(context.Background(), inst[0].submitIn(w, "BET", "10.00")); err != nil {
			t.Errorf("carteira %d: %v", i%wallets, err)
		}
	})
	for i, w := range ws {
		if bal, ver := inst[0].balance(t, w); bal != "50.00" || ver != 6 {
			t.Errorf("carteira %d: %s v%d", i, bal, ver)
		}
		assertReconciled(t, inst[0], w)
	}
}

// Uma carteira bloqueada não atrasa as demais (não há lock global).
func TestConcurrentLockedWalletDoesNotBlockOthers(t *testing.T) {
	inst := threeInstances(t)
	blocked := inst[0].openWallet(t, "100.00")
	free := inst[0].openWallet(t, "100.00")
	ctx := context.Background()

	held, release, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		holderDone <- inst[0].store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
			id, _ := inst[0].parseID(blocked.ID)
			if _, err := r.Wallets().GetForUpdate(ctx, id); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	blockedDone := make(chan error, 1)
	go func() {
		_, err := inst[1].txService().Submit(ctx, inst[0].submitIn(blocked, "BET", "10.00"))
		blockedDone <- err
	}()

	freeDone := make(chan error, 1)
	go func() {
		_, err := inst[2].txService().Submit(ctx, inst[0].submitIn(free, "BET", "10.00"))
		freeDone <- err
	}()

	select {
	case err := <-freeDone:
		if err != nil {
			t.Fatalf("carteira livre: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a carteira livre ficou esperando pela carteira bloqueada (lock global?)")
	}
	select {
	case err := <-blockedDone:
		t.Fatalf("a operação da carteira bloqueada não deveria ter terminado: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if err := <-blockedDone; err != nil {
		t.Fatalf("depois de liberar o lock: %v", err)
	}
	for _, w := range []app.Wallet{blocked, free} {
		if bal, _ := inst[0].balance(t, w); bal != "90.00" {
			t.Errorf("carteira %s: %s", w.ID, bal)
		}
		assertReconciled(t, inst[0], w)
	}
}

// REFUND e ROLLBACK simultâneos da mesma aposta: apenas um é processado.
func TestConcurrentRefundAndRollbackOfSameBet(t *testing.T) {
	inst := threeInstances(t)
	for round := range 20 {
		w := inst[0].openWallet(t, "100.00")
		bet := inst[0].submitIn(w, "BET", "25.00")
		inst[0].submit(t, bet)
		inputs := [2]app.SubmitInput{
			inst[0].submitIn(w, "REFUND", "25.00", withReference(bet.ExternalTransactionID)),
			inst[0].submitIn(w, "ROLLBACK", "25.00", withReference(bet.ExternalTransactionID)),
		}
		var results [2]app.TransactionResult
		concurrently(2, func(i int) {
			res, err := inst[(round+i)%3].txService().Submit(context.Background(), inputs[i])
			if err != nil {
				t.Errorf("rodada %d: %v", round, err)
			}
			results[i] = res
		})

		processed, already := 0, 0
		for _, r := range results {
			if r.Status == "PROCESSED" {
				processed++
			}
			if r.Status == "REJECTED" && r.FailureCode == "REFERENCE_ALREADY_REVERSED" {
				already++
			}
		}
		if processed != 1 || already != 1 {
			t.Fatalf("rodada %d: processadas=%d já-revertidas=%d (%+v)", round, processed, already, results)
		}
		if bal, _ := inst[0].balance(t, w); bal != "100.00" {
			t.Fatalf("rodada %d: saldo %s (devolução deve ocorrer uma única vez)", round, bal)
		}
		assertReconciled(t, inst[0], w)
	}
}
