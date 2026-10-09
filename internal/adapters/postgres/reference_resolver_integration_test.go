//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"desafio/internal/app"
	"desafio/internal/platform/logging"
)

// fakeClock é um relógio controlado pelo teste.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// outcomes conta os desfechos observados.
type outcomes struct {
	mu sync.Mutex
	m  map[string]int
}

func newOutcomes() *outcomes { return &outcomes{m: map[string]int{}} }

func (o *outcomes) observe(outcome string) {
	o.mu.Lock()
	o.m[outcome]++
	o.mu.Unlock()
}

func (o *outcomes) get(outcome string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m[outcome]
}

func testReferencePolicy() app.ReferencePolicy {
	return app.ReferencePolicy{
		MaxAttempts: 5, TTL: time.Hour, BaseDelay: time.Second, MaxDelay: 8 * time.Second,
		PollInterval: 10 * time.Millisecond, ClaimLease: 30 * time.Second, BatchSize: 20,
	}
}

// resolver cria um resolvedor com jitter neutro (fator 1,0) sobre o store deste ambiente.
func (e testEnv) resolver(clk app.Clock, policy app.ReferencePolicy, obs *outcomes, opts ...app.ResolverOption) *app.PendingReferenceResolver {
	all := []app.ResolverOption{app.WithJitter(func() float64 { return 0.5 }), app.WithOutcomeObserver(obs.observe)}
	return app.NewPendingReferenceResolver(e.store, e.ids, clk, policy, append(all, opts...)...)
}

func (e testEnv) batch(t *testing.T, r *app.PendingReferenceResolver) int {
	t.Helper()
	n, err := r.ResolveBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// extID devolve um id externo único.
func (e testEnv) extID() string { return string(e.ids.NewID()) }

// sendBet envia uma BET com o id externo informado (a referência de uma reversão pendente).
func (e testEnv) sendBet(t *testing.T, w app.Wallet, ext, amount string) app.TransactionResult {
	t.Helper()
	return e.submit(t, e.submitIn(w, "BET", amount, func(in *app.SubmitInput) { in.ExternalTransactionID = ext }))
}

func (e testEnv) status(t *testing.T, id string) (status string, failure string) {
	t.Helper()
	row := readSchedule(t, id)
	if row.failure != nil {
		failure = *row.failure
	}
	return row.status, failure
}

func TestResolverResumesRefundWhenReferenceArrives(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	betExt := env.extID()
	_, pending := env.pendingTx(t, w, "REFUND", "25.00", betExt)
	bet := env.sendBet(t, w, betExt, "25.00")

	obs := newOutcomes()
	clk := &fakeClock{t: baseTime().Add(time.Minute)}
	if n := env.batch(t, env.resolver(clk, testReferencePolicy(), obs)); n != 1 {
		t.Fatalf("reivindicadas = %d", n)
	}

	if st, _ := env.status(t, pending.TransactionID); st != "PROCESSED" {
		t.Fatalf("estado = %s", st)
	}
	if bal, ver := env.balance(t, w); bal != "100.00" || ver != 3 {
		t.Fatalf("carteira: %s v%d (esperado 100.00 v3)", bal, ver)
	}
	if n := env.ledgerCount(t, w); n != 3 { // abertura, aposta, reembolso
		t.Fatalf("lançamentos = %d", n)
	}
	var resolved string
	var next *time.Time
	if err := newPool(t).QueryRow(context.Background(),
		`SELECT resolved_reference_id::text, next_attempt_at FROM wager_transactions WHERE id = $1`, pending.TransactionID).Scan(&resolved, &next); err != nil {
		t.Fatal(err)
	}
	if resolved != bet.TransactionID || next != nil {
		t.Fatalf("referência resolvida=%s, próximo=%v", resolved, next)
	}
	if got, err := env.txService().GetByID(context.Background(), pending.TransactionID); err != nil || got.Status != "PROCESSED" {
		t.Fatalf("consulta: %+v, %v", got, err)
	}
	if obs.get("processed") != 1 {
		t.Errorf("desfechos: %v", obs.m)
	}
	// uma pendência concluída deixa de ser reivindicada
	if n := env.batch(t, env.resolver(clk, testReferencePolicy(), obs)); n != 0 {
		t.Errorf("segunda varredura reivindicou %d", n)
	}
	assertReconciled(t, env, w)
}

func TestResolverRollbackOfWinProcessedOrRejectedForFunds(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	clk := &fakeClock{t: baseTime().Add(time.Minute)}

	// saldo suficiente: o rollback da WIN debita
	w1 := env.openWallet(t, "100.00")
	winExt1 := env.extID()
	_, p1 := env.pendingTx(t, w1, "ROLLBACK", "50.00", winExt1)
	env.submit(t, env.submitIn(w1, "WIN", "50.00", func(in *app.SubmitInput) { in.ExternalTransactionID = winExt1 }))

	// saldo insuficiente: o ganho foi gasto antes
	w2 := env.openWallet(t, "0.00")
	winExt2 := env.extID()
	_, p2 := env.pendingTx(t, w2, "ROLLBACK", "50.00", winExt2)
	env.submit(t, env.submitIn(w2, "WIN", "50.00", func(in *app.SubmitInput) { in.ExternalTransactionID = winExt2 }))
	env.submit(t, env.submitIn(w2, "BET", "50.00"))

	if n := env.batch(t, env.resolver(clk, testReferencePolicy(), newOutcomes())); n != 2 {
		t.Fatalf("reivindicadas = %d", n)
	}
	if st, _ := env.status(t, p1.TransactionID); st != "PROCESSED" {
		t.Errorf("rollback com saldo: %s", st)
	}
	if bal, _ := env.balance(t, w1); bal != "100.00" {
		t.Errorf("saldo 1 = %s", bal)
	}
	if st, code := env.status(t, p2.TransactionID); st != "REJECTED" || code != "REVERSAL_INSUFFICIENT_FUNDS" {
		t.Errorf("rollback sem saldo: %s/%s", st, code)
	}
	if bal, _ := env.balance(t, w2); bal != "0.00" {
		t.Errorf("saldo 2 = %s", bal)
	}
	assertReconciled(t, env, w1)
	assertReconciled(t, env, w2)
}

// O contador e o agendamento persistidos fazem cada nova instância continuar de onde a anterior parou.
func TestResolverRetriesWithGrowingBackoffAcrossInstances(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	_, p := env.pendingTx(t, w, "REFUND", "25.00", env.extID()) // a referência nunca chega
	obs := newOutcomes()
	policy := testReferencePolicy()

	at := baseTime().Add(time.Second)
	wantDelays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} // teto de 8s
	for i, delay := range wantDelays {
		clk := &fakeClock{t: at}
		instance := newEnv(t) // uma nova instância (pool próprio) a cada passo
		if n := instance.batch(t, instance.resolver(clk, policy, obs)); n != 1 {
			t.Fatalf("passo %d: reivindicadas = %d", i+1, n)
		}
		row := readSchedule(t, p.TransactionID)
		if row.status != "PENDING_REFERENCE" || row.attempts != i+1 || row.next == nil || !row.next.Equal(at.Add(delay)) {
			t.Fatalf("passo %d: %+v (esperado tentativas=%d, próxima=%v)", i+1, row, i+1, at.Add(delay))
		}
		// ainda não venceu: nada a reivindicar
		if n := instance.batch(t, instance.resolver(clk, policy, obs)); n != 0 {
			t.Fatalf("passo %d: pendência não vencida foi reivindicada", i+1)
		}
		at = at.Add(delay)
	}
	if obs.get("retry") != 4 {
		t.Errorf("desfechos: %v", obs.m)
	}
	if bal, ver := env.balance(t, w); bal != "100.00" || ver != 1 {
		t.Errorf("a carteira não deveria mudar: %s v%d", bal, ver)
	}
}

func TestResolverExpiresByMaxAttempts(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	_, p := env.pendingTx(t, w, "REFUND", "25.00", env.extID())
	policy := testReferencePolicy()
	policy.MaxAttempts = 3
	obs := newOutcomes()
	clk := &fakeClock{t: baseTime().Add(time.Second)}
	r := env.resolver(clk, policy, obs)

	for step := 1; step <= 3; step++ {
		if n := env.batch(t, r); n != 1 {
			t.Fatalf("passo %d: reivindicadas = %d", step, n)
		}
		clk.Set(clk.Now().Add(time.Minute)) // além de qualquer atraso
	}
	st, code := env.status(t, p.TransactionID)
	if st != "REJECTED" || code != "REFERENCE_NOT_FOUND" {
		t.Fatalf("depois de esgotar as tentativas: %s/%s", st, code)
	}
	if obs.get("retry") != 2 || obs.get("expired") != 1 {
		t.Errorf("desfechos: %v", obs.m)
	}
	if bal, ver := env.balance(t, w); bal != "100.00" || ver != 1 || env.ledgerCount(t, w) != 1 {
		t.Errorf("a carteira não deveria mudar: %s v%d", bal, ver)
	}
	if n := env.batch(t, r); n != 0 {
		t.Errorf("pendência expirada voltou a ser reivindicada")
	}
}

func TestResolverExpiresByTTLWithAttemptsLeft(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	_, p := env.pendingTx(t, w, "ROLLBACK", "25.00", env.extID())
	policy := testReferencePolicy()
	clk := &fakeClock{t: baseTime().Add(policy.TTL + time.Minute)}

	if n := env.batch(t, env.resolver(clk, policy, newOutcomes())); n != 1 {
		t.Fatalf("reivindicadas = %d", n)
	}
	if st, code := env.status(t, p.TransactionID); st != "REJECTED" || code != "REFERENCE_NOT_FOUND" {
		t.Fatalf("TTL vencido: %s/%s", st, code)
	}
}

func TestResolverExpiresWithExistingButUnfinishedReference(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	// A aguarda uma referência que nunca chega; B é um ROLLBACK de A (que existe, mas está pendente).
	aIn, a := env.pendingTx(t, w, "REFUND", "25.00", env.extID())
	_, b := env.pendingTx(t, w, "ROLLBACK", "25.00", aIn.ExternalTransactionID)
	policy := testReferencePolicy()
	setNextAttempt(t, a.TransactionID, baseTime().Add(30*24*time.Hour)) // só B está vencida
	clk := &fakeClock{t: baseTime().Add(policy.TTL + time.Minute)}

	if n := env.batch(t, env.resolver(clk, policy, newOutcomes())); n != 1 {
		t.Fatalf("reivindicadas = %d (esperava só B)", n)
	}
	if st, code := env.status(t, b.TransactionID); st != "REJECTED" || code != "REFERENCE_NOT_PROCESSED" {
		t.Fatalf("referência pendente ao expirar: %s/%s", st, code)
	}
	if st, _ := env.status(t, a.TransactionID); st != "PENDING_REFERENCE" {
		t.Fatalf("A deveria continuar pendente: %s", st)
	}
}

func TestResolverRejectsImmediatelyWhenReferenceFailed(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "10.00")
	betExt := env.extID()
	_, p := env.pendingTx(t, w, "REFUND", "80.00", betExt)
	if res := env.sendBet(t, w, betExt, "80.00"); res.Status != "REJECTED" { // sem saldo
		t.Fatalf("aposta: %+v", res)
	}
	obs := newOutcomes()
	clk := &fakeClock{t: baseTime().Add(time.Minute)}

	if n := env.batch(t, env.resolver(clk, testReferencePolicy(), obs)); n != 1 {
		t.Fatalf("reivindicadas = %d", n)
	}
	if st, code := env.status(t, p.TransactionID); st != "REJECTED" || code != "REFERENCE_NOT_PROCESSED" {
		t.Fatalf("referência rejeitada: %s/%s", st, code)
	}
	if obs.get("rejected") != 1 || readSchedule(t, p.TransactionID).attempts != 0 {
		t.Errorf("rejeição imediata, sem tentativas: %v", obs.m)
	}
}

func TestResolverOutcomesAreObservedAndLogged(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	policy := testReferencePolicy()

	betExt := env.extID()
	_, resolvable := env.pendingTx(t, w, "REFUND", "10.00", betExt)
	env.sendBet(t, w, betExt, "10.00")
	_, retrying := env.pendingTx(t, w, "REFUND", "5.00", env.extID())
	_, expiring := env.pendingTx(t, w, "REFUND", "6.00", env.extID())
	execOK(t, newPool(t), `UPDATE wager_transactions SET created_at = created_at - interval '3 hours' WHERE id = $1`, expiring.TransactionID)

	var logs bytes.Buffer
	obs := newOutcomes()
	clk := &fakeClock{t: baseTime().Add(time.Minute)}
	r := env.resolver(clk, policy, obs, app.WithResolverLogger(logging.New(&logs, slog.LevelInfo)))
	if n := env.batch(t, r); n != 3 {
		t.Fatalf("reivindicadas = %d", n)
	}
	if obs.get("processed") != 1 || obs.get("retry") != 1 || obs.get("expired") != 1 {
		t.Fatalf("desfechos: %v", obs.m)
	}

	seen := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log fora de JSON: %q", line)
		}
		seen[m["outcome"].(string)] = m
	}
	for outcome, id := range map[string]string{"processed": resolvable.TransactionID, "retry": retrying.TransactionID, "expired": expiring.TransactionID} {
		m := seen[outcome]
		if m == nil || m["transactionId"] != id || m["walletId"] != w.ID {
			t.Errorf("log de %s: %v", outcome, m)
		}
	}
	if strings.Contains(logs.String(), "postgres://") || strings.Contains(strings.ToLower(logs.String()), "password") {
		t.Error("o log não deve conter credenciais")
	}
}

// Três instâncias disputam 30 pendências: cada uma é concluída exatamente uma vez.
func TestResolverThreeInstancesProcessEachPendingOnce(t *testing.T) {
	clearPendingReferences(t)
	inst := threeInstances(t)
	const total = 30
	wallets := make([]app.Wallet, total)
	pendings := make([]string, total)
	for i := range total {
		w := inst[0].openWallet(t, "100.00")
		wallets[i] = w
		betExt := inst[0].extID()
		_, p := inst[0].pendingTx(t, w, "REFUND", "10.00", betExt)
		pendings[i] = p.TransactionID
		inst[0].sendBet(t, w, betExt, "10.00")
	}

	policy := testReferencePolicy()
	policy.BatchSize = 7
	obs := newOutcomes()
	clk := &fakeClock{t: baseTime().Add(time.Minute)}
	var wg sync.WaitGroup
	for i := range inst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := inst[i].resolver(clk, policy, obs)
			for {
				n, err := r.ResolveBatch(context.Background())
				if err != nil {
					t.Errorf("instância %d: %v", i, err)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	if obs.get("processed") != total {
		t.Fatalf("processadas = %d, esperado %d (%v)", obs.get("processed"), total, obs.m)
	}
	for i, w := range wallets {
		if st, _ := inst[0].status(t, pendings[i]); st != "PROCESSED" {
			t.Errorf("pendência %d: %s", i, st)
		}
		if bal, ver := inst[0].balance(t, w); bal != "100.00" || ver != 3 {
			t.Errorf("carteira %d: %s v%d (reembolso aplicado mais de uma vez?)", i, bal, ver)
		}
		if n := inst[0].ledgerCount(t, w); n != 3 {
			t.Errorf("carteira %d: %d lançamentos", i, n)
		}
		assertReconciled(t, inst[0], w)
	}
}

func TestResolverRecoversAbandonedClaims(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	betExt := env.extID()
	_, p := env.pendingTx(t, w, "REFUND", "25.00", betExt)
	env.sendBet(t, w, betExt, "25.00")
	policy := testReferencePolicy()
	t0 := baseTime().Add(time.Minute)

	// uma instância reivindica e "cai" sem processar
	crashed := env.claim(t, t0, t0.Add(policy.ClaimLease), 10)
	if len(crashed) != 1 || crashed[0].ID.String() != p.TransactionID {
		t.Fatalf("reivindicação: %+v", crashed)
	}

	survivor := newEnv(t)
	clk := &fakeClock{t: t0}
	r := survivor.resolver(clk, policy, newOutcomes())
	if n := survivor.batch(t, r); n != 0 {
		t.Fatalf("dentro da lease a pendência pertence à outra instância (reivindicadas %d)", n)
	}
	clk.Set(t0.Add(policy.ClaimLease + time.Second))
	if n := survivor.batch(t, r); n != 1 {
		t.Fatalf("depois da lease a pendência deveria ser reassumida (reivindicadas %d)", n)
	}
	if st, _ := env.status(t, p.TransactionID); st != "PROCESSED" {
		t.Fatalf("estado = %s", st)
	}
}

func TestResolveItemIgnoresAlreadyResolved(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	betExt := env.extID()
	_, p := env.pendingTx(t, w, "REFUND", "25.00", betExt)
	env.sendBet(t, w, betExt, "25.00") // saldo 75.00

	t0 := baseTime().Add(time.Minute)
	items := env.claim(t, t0, t0.Add(time.Minute), 10)
	if len(items) != 1 {
		t.Fatalf("reivindicadas: %d", len(items))
	}
	// outro caminho conclui a transação entre a reivindicação e o processamento
	execOK(t, newPool(t), `UPDATE wager_transactions SET status = 'PROCESSED', result_balance = 7500, next_attempt_at = NULL WHERE id = $1`, p.TransactionID)

	obs := newOutcomes()
	if err := env.resolver(&fakeClock{t: t0}, testReferencePolicy(), obs).ResolveItem(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	if bal, ver := env.balance(t, w); bal != "75.00" || ver != 2 {
		t.Fatalf("a carteira não deveria mudar: %s v%d", bal, ver)
	}
	if len(obs.m) != 0 {
		t.Errorf("nenhum desfecho esperado: %v", obs.m)
	}
}

// O laço com o resolvedor real retoma a pendência sozinho (relógio real) e termina ao cancelar o contexto.
func TestWorkerLoopResumesPendingOnItsOwn(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	betExt := env.extID()
	_, pending := env.pendingTx(t, w, "REFUND", "25.00", betExt)
	env.sendBet(t, w, betExt, "25.00")

	policy := testReferencePolicy()
	policy.PollInterval = 20 * time.Millisecond
	resolver := app.NewPendingReferenceResolver(env.store, env.ids, env.clock, policy)
	worker := app.NewPendingReferenceWorker(resolver, policy, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, _ := env.status(t, pending.TransactionID); st == "PROCESSED" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("a pendência não foi retomada pelo laço")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if bal, _ := env.balance(t, w); bal != "100.00" {
		t.Errorf("saldo = %s", bal)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("o laço não terminou depois do cancelamento")
	}
}

// Depois que o worker conclui a pendência, o replay da requisição original reflete o estado atual.
func TestReplayAfterWorkerResolvedPending(t *testing.T) {
	clearPendingReferences(t)
	env := newEnv(t)
	ctx := context.Background()
	w := env.openWallet(t, "100.00")
	svc := app.NewIdempotentTransactions(postgresIdempotencyStore(t), env.txService())

	betExt := env.extID()
	in := env.submitIn(w, "REFUND", "25.00", withReference(betExt))
	first, err := svc.Submit(ctx, in)
	if err != nil || first.Status != "PENDING_REFERENCE" || first.IdempotentReplay {
		t.Fatalf("primeira resposta: %+v, %v", first, err)
	}
	// ainda pendente: o replay continua pendente, com o mesmo id e sem nova movimentação
	again, err := svc.Submit(ctx, in)
	if err != nil || again.Status != "PENDING_REFERENCE" || !again.IdempotentReplay || again.TransactionID != first.TransactionID {
		t.Fatalf("replay da pendência aberta: %+v, %v", again, err)
	}

	env.sendBet(t, w, betExt, "25.00")
	clk := &fakeClock{t: baseTime().Add(time.Minute)}
	if n := env.batch(t, env.resolver(clk, testReferencePolicy(), newOutcomes())); n != 1 {
		t.Fatalf("reivindicadas = %d", n)
	}

	replay, err := svc.Submit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Status != "PROCESSED" || replay.Balance == nil || replay.Balance.Amount != "100.00" ||
		!replay.IdempotentReplay || replay.TransactionID != first.TransactionID {
		t.Fatalf("replay depois da conclusão: %+v", replay)
	}
	if bal, ver := env.balance(t, w); bal != "100.00" || ver != 3 {
		t.Fatalf("o replay não deveria mexer na carteira: %s v%d", bal, ver)
	}

	// o conflito de conteúdo continua detectado para a chave de uma operação que era pendente
	other := in
	other.Money.Amount = "26.00"
	if _, err := svc.Submit(ctx, other); err == nil {
		t.Error("a mesma chave com outro conteúdo deveria ser rejeitada")
	}
}
