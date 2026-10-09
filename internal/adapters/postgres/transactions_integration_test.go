//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
)

func (e testEnv) txService() *app.TransactionService {
	return app.NewTransactionService(e.store, e.ids, e.clock)
}

// openWallet abre uma carteira pelo caso de uso e a devolve.
func (e testEnv) openWallet(t *testing.T, amount string) app.Wallet {
	t.Helper()
	w, err := e.walletService().Open(context.Background(), openInput(string(e.ids.NewID()), amount))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// submitIn monta uma operação válida com id externo e chave únicos.
func (e testEnv) submitIn(w app.Wallet, kind, amount string, mutate ...func(*app.SubmitInput)) app.SubmitInput {
	ext := string(e.ids.NewID())
	in := app.SubmitInput{
		ProviderID: "prov", ExternalTransactionID: ext, PlayerID: w.PlayerID, WalletID: w.ID,
		RoundID: "round-1", GameID: "game-1", Kind: kind, Money: app.Money{Amount: amount, Currency: "BRL"},
		IdempotencyKey: "key-" + ext,
	}
	for _, m := range mutate {
		m(&in)
	}
	return in
}

func (e testEnv) submit(t *testing.T, in app.SubmitInput) app.TransactionResult {
	t.Helper()
	res, err := e.txService().Submit(context.Background(), in)
	if err != nil {
		t.Fatalf("Submit %s %s: %v", in.Kind, in.Money.Amount, err)
	}
	return res
}

func (e testEnv) balance(t *testing.T, w app.Wallet) (string, int64) {
	t.Helper()
	got, err := e.walletService().Get(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Balance.Amount, got.Version
}

func (e testEnv) ledgerCount(t *testing.T, w app.Wallet) int {
	t.Helper()
	return countRows(t, e, `SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, w.ID)
}

func TestSubmitBetProcessed(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "1000.00")
	in := env.submitIn(w, "BET", "25.00")

	res := env.submit(t, in)
	if res.Status != "PROCESSED" || res.Balance == nil || res.Balance.Amount != "975.00" || res.IdempotentReplay || res.FailureCode != "" {
		t.Fatalf("resultado: %+v", res)
	}
	if bal, ver := env.balance(t, w); bal != "975.00" || ver != 2 {
		t.Fatalf("carteira: %s v%d", bal, ver)
	}
	page, _ := env.walletService().Ledger(context.Background(), w.ID, "", 10)
	if len(page.Entries) != 2 || page.Entries[1].Direction != "DEBIT" || page.Entries[1].BalanceAfter.Amount != "975.00" || page.Entries[1].TransactionID != res.TransactionID {
		t.Fatalf("ledger: %+v", page.Entries)
	}
	got, err := env.txService().GetByID(context.Background(), res.TransactionID)
	if err != nil || got.Status != "PROCESSED" || got.Kind != "BET" || got.ExternalTransactionID != in.ExternalTransactionID {
		t.Fatalf("transação: %+v, %v", got, err)
	}
}

func TestSubmitWinAndLoss(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")

	win := env.submit(t, env.submitIn(w, "WIN", "50.00"))
	if win.Status != "PROCESSED" || win.Balance.Amount != "150.00" {
		t.Fatalf("WIN: %+v", win)
	}
	loss := env.submit(t, env.submitIn(w, "LOSS", "0.00"))
	if loss.Status != "PROCESSED" || loss.Balance == nil || loss.Balance.Amount != "150.00" {
		t.Fatalf("LOSS: %+v", loss)
	}
	if bal, ver := env.balance(t, w); bal != "150.00" || ver != 2 { // a LOSS não altera a versão
		t.Fatalf("carteira: %s v%d", bal, ver)
	}
	if n := env.ledgerCount(t, w); n != 2 { // abertura + WIN; a LOSS não gera lançamento
		t.Fatalf("lançamentos = %d", n)
	}
}

func TestSubmitRejectedForInsufficientFundsIsAudited(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "20.00")
	in := env.submitIn(w, "BET", "80.00")

	res := env.submit(t, in)
	if res.Status != "REJECTED" || res.FailureCode != "INSUFFICIENT_FUNDS" || res.Balance != nil {
		t.Fatalf("resultado: %+v", res)
	}
	if bal, ver := env.balance(t, w); bal != "20.00" || ver != 1 {
		t.Fatalf("carteira mudou: %s v%d", bal, ver)
	}
	if n := env.ledgerCount(t, w); n != 1 {
		t.Fatalf("lançamentos = %d", n)
	}
	got, err := env.txService().GetByExternal(context.Background(), in.ProviderID, in.ExternalTransactionID)
	if err != nil || got.Status != "REJECTED" || got.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Fatalf("rejeição auditável: %+v, %v", got, err)
	}
}

func TestSubmitWalletOrPlayerMismatchIsRejected(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	res := env.submit(t, env.submitIn(w, "BET", "10.00", func(in *app.SubmitInput) { in.PlayerID = string(env.ids.NewID()) }))
	if res.Status != "REJECTED" || res.FailureCode != "WALLET_MISMATCH" {
		t.Fatalf("resultado: %+v", res)
	}
	usd := env.submitIn(w, "BET", "10.00", func(in *app.SubmitInput) { in.Money.Currency = "USD" })
	if res := env.submit(t, usd); res.Status != "REJECTED" || res.FailureCode != "WALLET_MISMATCH" {
		t.Fatalf("moeda: %+v", res)
	}
	if bal, ver := env.balance(t, w); bal != "100.00" || ver != 1 {
		t.Fatalf("carteira mudou: %s v%d", bal, ver)
	}
}

func TestSubmitUnknownWalletWritesNothing(t *testing.T) {
	env := newEnv(t)
	ghost := app.Wallet{ID: string(env.ids.NewID()), PlayerID: string(env.ids.NewID())}
	in := env.submitIn(ghost, "BET", "10.00")
	if _, err := env.txService().Submit(context.Background(), in); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("esperava ErrNotFound, obtido %v", err)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wager_transactions WHERE external_transaction_id = $1`, in.ExternalTransactionID); n != 0 {
		t.Errorf("transações gravadas = %d", n)
	}
}

func TestSubmitInvalidAmountForKind(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	for name, in := range map[string]app.SubmitInput{
		"BET zero":    env.submitIn(w, "BET", "0.00"),
		"WIN zero":    env.submitIn(w, "WIN", "0.00"),
		"LOSS valor":  env.submitIn(w, "LOSS", "1.00"),
		"formato":     env.submitIn(w, "BET", "10"),
		"id inválido": env.submitIn(w, "BET", "1.00", func(in *app.SubmitInput) { in.WalletID = "x" }),
		"tipo":        env.submitIn(w, "OPENING", "1.00"),
	} {
		if _, err := env.txService().Submit(context.Background(), in); !errors.Is(err, app.ErrInvalidInput) {
			t.Errorf("%s: erro %v, esperado ErrInvalidInput", name, err)
		}
	}
	if n := countRows(t, env, `SELECT count(*) FROM wager_transactions WHERE wallet_id = $1`, w.ID); n != 1 { // só a OPENING
		t.Errorf("transações = %d", n)
	}
}

func withReference(ext string) func(*app.SubmitInput) {
	return func(in *app.SubmitInput) { in.ReferenceExternalTransactionID = ext }
}

func TestSubmitRefundOfProcessedBet(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "1000.00")
	bet := env.submitIn(w, "BET", "25.00")
	env.submit(t, bet)

	refund := env.submit(t, env.submitIn(w, "REFUND", "25.00", withReference(bet.ExternalTransactionID)))
	if refund.Status != "PROCESSED" || refund.Balance.Amount != "1000.00" {
		t.Fatalf("REFUND: %+v", refund)
	}
	if bal, ver := env.balance(t, w); bal != "1000.00" || ver != 3 {
		t.Fatalf("carteira: %s v%d", bal, ver)
	}
	var resolved string
	if err := newPool(t).QueryRow(context.Background(), `SELECT resolved_reference_id::text FROM wager_transactions WHERE id = $1`, refund.TransactionID).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	betTx, _ := env.txService().GetByExternal(context.Background(), bet.ProviderID, bet.ExternalTransactionID)
	if resolved != betTx.ID {
		t.Errorf("referência resolvida = %s, esperado %s", resolved, betTx.ID)
	}
}

func TestSubmitRollbackOfBetWinAndRefund(t *testing.T) {
	env := newEnv(t)
	for _, tc := range []struct {
		name, kind, want string
	}{
		{"BET devolve o débito", "BET", "100.00"},
		{"WIN desfaz o crédito", "WIN", "100.00"},
	} {
		w := env.openWallet(t, "100.00")
		orig := env.submitIn(w, tc.kind, "25.00")
		env.submit(t, orig)
		res := env.submit(t, env.submitIn(w, "ROLLBACK", "25.00", withReference(orig.ExternalTransactionID)))
		if res.Status != "PROCESSED" || res.Balance.Amount != tc.want {
			t.Errorf("%s: %+v", tc.name, res)
		}
	}

	// ROLLBACK de um REFUND processado: re-debita o valor
	w := env.openWallet(t, "100.00")
	bet := env.submitIn(w, "BET", "25.00")
	env.submit(t, bet)
	refund := env.submitIn(w, "REFUND", "25.00", withReference(bet.ExternalTransactionID))
	env.submit(t, refund)
	res := env.submit(t, env.submitIn(w, "ROLLBACK", "25.00", withReference(refund.ExternalTransactionID)))
	if res.Status != "PROCESSED" || res.Balance.Amount != "75.00" {
		t.Errorf("rollback de refund: %+v", res)
	}
}

func TestSubmitReversalBeforeReferenceIsPersistedAsPending(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	in := env.submitIn(w, "ROLLBACK", "25.00", withReference("ainda-nao-chegou"))

	res := env.submit(t, in)
	if res.Status != "PENDING_REFERENCE" || res.Balance != nil || res.FailureCode != "" {
		t.Fatalf("resultado: %+v", res)
	}
	if bal, ver := env.balance(t, w); bal != "100.00" || ver != 1 {
		t.Fatalf("carteira mudou: %s v%d", bal, ver)
	}
	got, err := env.txService().GetByID(context.Background(), res.TransactionID)
	if err != nil || got.Status != "PENDING_REFERENCE" {
		t.Fatalf("pendência consultável: %+v, %v", got, err)
	}
}

func TestSubmitReversalOfRejectedReference(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "10.00")
	bet := env.submitIn(w, "BET", "80.00") // rejeitada por saldo
	if res := env.submit(t, bet); res.Status != "REJECTED" {
		t.Fatalf("aposta: %+v", res)
	}
	res := env.submit(t, env.submitIn(w, "REFUND", "80.00", withReference(bet.ExternalTransactionID)))
	if res.Status != "REJECTED" || res.FailureCode != "REFERENCE_NOT_PROCESSED" {
		t.Fatalf("resultado: %+v", res)
	}
}

func TestSubmitReversalWithoutFundsHasDistinctCode(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "0.00")
	win := env.submitIn(w, "WIN", "50.00")
	env.submit(t, win)
	env.submit(t, env.submitIn(w, "BET", "50.00")) // gasta o ganho
	res := env.submit(t, env.submitIn(w, "ROLLBACK", "50.00", withReference(win.ExternalTransactionID)))
	if res.Status != "REJECTED" || res.FailureCode != "REVERSAL_INSUFFICIENT_FUNDS" {
		t.Fatalf("resultado: %+v", res)
	}
	if bal, _ := env.balance(t, w); bal != "0.00" {
		t.Fatalf("saldo = %s", bal)
	}
}

func TestSubmitSecondReversalOfSameBetIsRejected(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	bet := env.submitIn(w, "BET", "25.00")
	env.submit(t, bet)
	env.submit(t, env.submitIn(w, "REFUND", "25.00", withReference(bet.ExternalTransactionID)))

	res := env.submit(t, env.submitIn(w, "ROLLBACK", "25.00", withReference(bet.ExternalTransactionID)))
	if res.Status != "REJECTED" || res.FailureCode != "REFERENCE_ALREADY_REVERSED" {
		t.Fatalf("resultado: %+v", res)
	}
	if bal, _ := env.balance(t, w); bal != "100.00" { // devolvido uma única vez
		t.Fatalf("saldo = %s", bal)
	}
}

func TestSubmitReplayAndConflicts(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.openWallet(t, "1000.00")
	in := env.submitIn(w, "BET", "25.00")
	first := env.submit(t, in)

	// outras movimentações depois do processamento original
	env.submit(t, env.submitIn(w, "BET", "100.00"))

	replay := env.submit(t, in)
	if !replay.IdempotentReplay || replay.TransactionID != first.TransactionID || replay.Balance.Amount != "975.00" {
		t.Fatalf("replay deve devolver o saldo observado no original: %+v", replay)
	}
	if bal, ver := env.balance(t, w); bal != "875.00" || ver != 3 {
		t.Fatalf("o replay mexeu na carteira: %s v%d", bal, ver)
	}

	otherKey := in
	otherKey.IdempotencyKey = "outra-chave"
	if _, err := env.txService().Submit(ctx, otherKey); !errors.Is(err, app.ErrIdempotencyConflict) {
		t.Errorf("mesma operação, outra chave: %v", err)
	}
	otherBody := in
	otherBody.Money.Amount = "26.00"
	if _, err := env.txService().Submit(ctx, otherBody); !errors.Is(err, app.ErrIdempotencyConflict) {
		t.Errorf("mesma chave, conteúdo diferente: %v", err)
	}
	if n := countRows(t, env, `SELECT count(*) FROM wager_transactions WHERE external_transaction_id = $1`, in.ExternalTransactionID); n != 1 {
		t.Errorf("transações da operação = %d", n)
	}
}

func TestSubmitReplayOfRejectedKeepsFailureCode(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "5.00")
	in := env.submitIn(w, "BET", "80.00")
	env.submit(t, in)
	replay := env.submit(t, in)
	if !replay.IdempotentReplay || replay.Status != "REJECTED" || replay.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Fatalf("replay: %+v", replay)
	}
}

func TestSubmitThroughIdempotencyDecorator(t *testing.T) {
	env := newEnv(t)
	w := env.openWallet(t, "100.00")
	svc := app.NewIdempotentTransactions(postgresIdempotencyStore(t), env.txService())
	in := env.submitIn(w, "BET", "10.00")

	first, err := svc.Submit(context.Background(), in)
	if err != nil || first.IdempotentReplay {
		t.Fatalf("primeira: %+v, %v", first, err)
	}
	second, err := svc.Submit(context.Background(), in)
	if err != nil || !second.IdempotentReplay || second.TransactionID != first.TransactionID {
		t.Fatalf("segunda: %+v, %v", second, err)
	}
	if bal, _ := env.balance(t, w); bal != "90.00" {
		t.Fatalf("saldo = %s", bal)
	}
}

func TestQueriesByIDAndExternal(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	w := env.openWallet(t, "10.00")
	rejected := env.submitIn(w, "BET", "80.00")
	rres := env.submit(t, rejected)
	pending := env.submitIn(w, "REFUND", "1.00", withReference("depois"))
	pres := env.submit(t, pending)

	got, err := env.txService().GetByID(ctx, rres.TransactionID)
	if err != nil || got.Status != "REJECTED" || got.FailureCode != "INSUFFICIENT_FUNDS" || got.WalletID != w.ID {
		t.Fatalf("rejeitada: %+v, %v", got, err)
	}
	got, err = env.txService().GetByExternal(ctx, pending.ProviderID, pending.ExternalTransactionID)
	if err != nil || got.ID != pres.TransactionID || got.Status != "PENDING_REFERENCE" {
		t.Fatalf("pendente: %+v, %v", got, err)
	}
	if _, err := env.txService().GetByID(ctx, string(env.ids.NewID())); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("id inexistente: %v", err)
	}
	if _, err := env.txService().GetByExternal(ctx, "prov", "nao-existe"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("externo inexistente: %v", err)
	}
	if _, err := env.txService().GetByID(ctx, "abc"); !errors.Is(err, app.ErrInvalidInput) {
		t.Errorf("id inválido: %v", err)
	}
}

func postgresIdempotencyStore(t *testing.T) *postgres.IdempotencyStore {
	t.Helper()
	return postgres.NewIdempotencyStore(newPool(t), 30*time.Second)
}
