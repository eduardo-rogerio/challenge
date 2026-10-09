//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"desafio/internal/app"
	"desafio/internal/domain"
)

// clearPendingReferences encerra as pendências deixadas por outros testes para que os desfechos aqui
// dependam apenas dos dados do próprio teste (a base de testes é compartilhada).
func clearPendingReferences(t *testing.T) {
	t.Helper()
	execOK(t, newPool(t), `UPDATE wager_transactions SET status = 'REJECTED', failure_code = 'TEST_CLEANUP', next_attempt_at = NULL
		WHERE status = 'PENDING_REFERENCE'`)
}

func baseTime() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// pendingTx cria, pelo caso de uso, uma reversão que aguarda a referência `ref` e devolve o resultado.
func (e testEnv) pendingTx(t *testing.T, w app.Wallet, kind, amount, ref string) (app.SubmitInput, app.TransactionResult) {
	t.Helper()
	in := e.submitIn(w, kind, amount, withReference(ref))
	res := e.submit(t, in)
	if res.Status != "PENDING_REFERENCE" {
		t.Fatalf("esperava PENDING_REFERENCE, obtido %+v", res)
	}
	return in, res
}

type scheduleRow struct {
	status   string
	attempts int
	next     *time.Time
	failure  *string
}

func readSchedule(t *testing.T, id string) scheduleRow {
	t.Helper()
	var r scheduleRow
	err := newPool(t).QueryRow(context.Background(),
		`SELECT status, reference_attempts, next_attempt_at, failure_code FROM wager_transactions WHERE id = $1`, id).
		Scan(&r.status, &r.attempts, &r.next, &r.failure)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func setNextAttempt(t *testing.T, id string, at time.Time) {
	t.Helper()
	execOK(t, newPool(t), `UPDATE wager_transactions SET next_attempt_at = $2 WHERE id = $1`, id, at)
}

func (e testEnv) claim(t *testing.T, now, until time.Time, limit int) []app.PendingReference {
	t.Helper()
	var items []app.PendingReference
	err := e.store.InTx(context.Background(), func(ctx context.Context, r app.Repos) error {
		var err error
		items, err = r.PendingReferences().Claim(ctx, now, until, limit)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestInsertPendingReferenceSetsSchedule(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	_, res := env.pendingTx(t, w, "REFUND", "25.00", "ainda-nao-chegou")

	row := readSchedule(t, res.TransactionID)
	if row.status != "PENDING_REFERENCE" || row.attempts != 0 || row.next == nil {
		t.Fatalf("agendamento inicial: %+v", row)
	}
}

func TestUpdateOutcomeEndsThePending(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.openWallet(t, "100.00")
	_, res := env.pendingTx(t, w, "REFUND", "25.00", "ref")

	id := domain.ID(res.TransactionID)
	err := env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		tx, err := r.Transactions().GetByID(ctx, id)
		if err != nil {
			return err
		}
		// ainda pendente: UpdateOutcome exige um desfecho
		if err := r.Transactions().UpdateOutcome(ctx, tx); err == nil {
			t.Error("UpdateOutcome de uma transação ainda pendente deveria falhar")
		}
		if err := tx.Reject(domain.FailureReferenceNotFound, env.clock.Now().Add(time.Second)); err != nil {
			return err
		}
		return r.Transactions().UpdateOutcome(ctx, tx)
	})
	if err != nil {
		t.Fatal(err)
	}

	row := readSchedule(t, res.TransactionID)
	if row.status != "REJECTED" || row.next != nil || row.failure == nil || *row.failure != "REFERENCE_NOT_FOUND" {
		t.Fatalf("depois do desfecho: %+v", row)
	}

	// uma segunda atualização encontra a transação já resolvida
	err = env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		tx, err := r.Transactions().GetByID(ctx, id)
		if err != nil {
			return err
		}
		stale := rehydrateAsRejected(t, tx)
		return r.Transactions().UpdateOutcome(ctx, stale)
	})
	if !errors.Is(err, app.ErrStaleTransaction) {
		t.Fatalf("esperava ErrStaleTransaction, obtido %v", err)
	}
}

// rehydrateAsRejected devolve uma cópia rejeitada da transação, para simular uma segunda atualização.
func rehydrateAsRejected(t *testing.T, tx *domain.WagerTransaction) *domain.WagerTransaction {
	t.Helper()
	snap := domain.TransactionSnapshot{
		ID: tx.ID(), ProviderID: tx.ProviderID(), ExternalID: tx.ExternalID(), IdempotencyKey: tx.IdempotencyKey(),
		RequestHash: tx.RequestHash(), WalletID: tx.WalletID(), PlayerID: tx.PlayerID(), RoundID: tx.RoundID(), GameID: tx.GameID(),
		Kind: tx.Kind(), Money: tx.Money(), ReferenceExternalID: tx.ReferenceExternalID(),
		Status: domain.StatusRejected, FailureCode: domain.FailureReferenceNotFound,
		CreatedAt: tx.CreatedAt(), UpdatedAt: tx.UpdatedAt(),
	}
	out, err := domain.RehydrateTransaction(snap)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClaimOnlyDueInOrderWithLease(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	base := baseTime()

	_, p1 := env.pendingTx(t, w, "REFUND", "1.00", "r1")
	_, p2 := env.pendingTx(t, w, "REFUND", "2.00", "r2")
	_, p3 := env.pendingTx(t, w, "REFUND", "3.00", "r3")
	setNextAttempt(t, p1.TransactionID, base.Add(-10*time.Minute))
	setNextAttempt(t, p2.TransactionID, base.Add(-5*time.Minute))
	setNextAttempt(t, p3.TransactionID, base.Add(time.Hour))

	lease := 30 * time.Second
	got := env.claim(t, base, base.Add(lease), 10)
	if len(got) != 2 || got[0].ID.String() != p1.TransactionID || got[1].ID.String() != p2.TransactionID {
		t.Fatalf("esperava p1 e p2 em ordem de vencimento, obtido %+v", got)
	}
	if got[0].WalletID.String() != w.ID || got[0].Attempts != 0 {
		t.Errorf("dados da reivindicação: %+v", got[0])
	}
	if next := readSchedule(t, p1.TransactionID).next; next == nil || !next.Equal(base.Add(lease)) {
		t.Errorf("a lease deveria adiar o agendamento para %v, obtido %v", base.Add(lease), next)
	}
	if again := env.claim(t, base, base.Add(lease), 10); len(again) != 0 {
		t.Fatalf("pendências sob lease não devem ser reivindicadas de novo: %+v", again)
	}
	// vencida a lease, voltam a ser reivindicáveis; o limite do lote é respeitado
	if one := env.claim(t, base.Add(lease+time.Second), base.Add(2*lease), 1); len(one) != 1 || one[0].ID.String() != p1.TransactionID {
		t.Fatalf("depois da lease, com limite 1: %+v", one)
	}
}

func TestClaimConcurrentClaimersDoNotOverlap(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	const total = 40
	want := map[string]bool{}
	for i := range total {
		_, p := env.pendingTx(t, w, "REFUND", "1.00", "ref-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		want[p.TransactionID] = true
	}
	base := baseTime().Add(time.Minute)

	var mu sync.Mutex
	claimed := map[string]int{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := newEnv(t) // pool próprio: uma instância
			for {
				items := e.claim(t, base, base.Add(time.Hour), 7)
				if len(items) == 0 {
					return
				}
				mu.Lock()
				for _, it := range items {
					claimed[it.ID.String()]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != total {
		t.Fatalf("reivindicadas %d de %d", len(claimed), total)
	}
	for id, n := range claimed {
		if n != 1 || !want[id] {
			t.Errorf("%s reivindicada %d vezes (esperada: 1)", id, n)
		}
	}
}

func TestRescheduleUpdatesAttemptsAndNext(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	ctx := context.Background()
	w := env.openWallet(t, "100.00")
	_, p := env.pendingTx(t, w, "REFUND", "1.00", "r")
	next := baseTime().Add(time.Hour)

	err := env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		return r.PendingReferences().Reschedule(ctx, domain.ID(p.TransactionID), 3, next)
	})
	if err != nil {
		t.Fatal(err)
	}
	row := readSchedule(t, p.TransactionID)
	if row.attempts != 3 || row.next == nil || !row.next.Equal(next) {
		t.Fatalf("agendamento: %+v", row)
	}

	// uma transação que não está pendente não pode ser reagendada
	bet := env.submitIn(w, "BET", "1.00")
	res := env.submit(t, bet)
	err = env.store.InTx(ctx, func(ctx context.Context, r app.Repos) error {
		return r.PendingReferences().Reschedule(ctx, domain.ID(res.TransactionID), 1, next)
	})
	if !errors.Is(err, app.ErrStaleTransaction) {
		t.Fatalf("esperava ErrStaleTransaction, obtido %v", err)
	}
}
