package domain

import (
	"errors"
	"testing"
	"time"
)

var tApply = t0.Add(time.Minute)

func apply(t *testing.T, w *Wallet, tx *WagerTransaction, ref Reference) (*WalletLedgerEntry, error) {
	t.Helper()
	return Apply(w, tx, ref, idEntry1, tApply)
}

// processedRef devolve uma transação já PROCESSED, para servir de referência.
func processedRef(t *testing.T, kind Kind, amount string, mutate ...func(*ExternalParams)) *WagerTransaction {
	t.Helper()
	m := append([]func(*ExternalParams){func(p *ExternalParams) { p.ID = idTx2; p.ExternalID = "ref-1" }}, mutate...)
	tx := newTx(t, kind, amount, m...)
	if err := tx.MarkProcessed(mustMoney(t, "1.00"), t0); err != nil {
		t.Fatal(err)
	}
	return tx
}

func mustRejected(t *testing.T, tx *WagerTransaction, code FailureCode) {
	t.Helper()
	if tx.Status() != StatusRejected || tx.FailureCode() != code {
		t.Fatalf("transação %s/%s, esperado REJECTED/%s", tx.Status(), tx.FailureCode(), code)
	}
}

func mustUntouched(t *testing.T, w *Wallet, balance string, version int64) {
	t.Helper()
	if w.Balance().Amount() != balance || w.Version() != version {
		t.Fatalf("carteira %s v%d, esperado %s v%d", w.Balance(), w.Version(), balance, version)
	}
}

// --- BET, WIN, LOSS ---

func TestApplyBetProcessed(t *testing.T) {
	w, tx := newWallet(t, "1000.00"), newTx(t, KindBet, "25.00")
	e, err := apply(t, w, tx, Reference{})
	if err != nil || e == nil {
		t.Fatalf("Apply: %v, %v", e, err)
	}
	mustUntouched(t, w, "975.00", 2)
	if tx.Status() != StatusProcessed || tx.FailureCode() != "" {
		t.Fatalf("transação %s/%s", tx.Status(), tx.FailureCode())
	}
	if b, ok := tx.ResultBalance(); !ok || b.Amount() != "975.00" {
		t.Fatalf("saldo observado %v, %v", b, ok)
	}
	if e.Direction() != DirectionDebit || e.TransactionID() != tx.ID() || e.WalletID() != w.ID() || e.Money().Amount() != "25.00" {
		t.Fatalf("lançamento %+v", e)
	}
}

func TestApplyBetInsufficientFunds(t *testing.T) {
	w, tx := newWallet(t, "20.00"), newTx(t, KindBet, "80.00")
	e, err := apply(t, w, tx, Reference{})
	if err != nil || e != nil {
		t.Fatalf("rejeição de negócio não é erro: %v, %v", e, err)
	}
	mustRejected(t, tx, FailureInsufficientFunds)
	mustUntouched(t, w, "20.00", 1)
}

func TestApplyWin(t *testing.T) {
	w, tx := newWallet(t, "100.00"), newTx(t, KindWin, "50.00")
	e, err := apply(t, w, tx, Reference{})
	if err != nil || e == nil || e.Direction() != DirectionCredit {
		t.Fatalf("Apply: %v, %v", e, err)
	}
	mustUntouched(t, w, "150.00", 2)
	if tx.Status() != StatusProcessed {
		t.Fatalf("estado %s", tx.Status())
	}
}

func TestApplyWinOptionalReference(t *testing.T) {
	withRef := func(p *ExternalParams) { p.ReferenceExternalID = "ref-1" }
	cases := []struct {
		name string
		ref  Reference
		want FailureCode // vazio = processada
	}{
		{"sem referência resolvida", Reference{}, ""},
		{"referência pendente não bloqueia", Reference{Tx: newTx(t, KindBet, "10.00", func(p *ExternalParams) { p.ID = idTx2 })}, ""},
		{"BET da mesma rodada", Reference{Tx: processedRef(t, KindBet, "10.00")}, ""},
		{"outra rodada", Reference{Tx: processedRef(t, KindBet, "10.00", func(p *ExternalParams) { p.RoundID = "round-2" })}, FailureReferenceMismatch},
		{"não é BET", Reference{Tx: processedRef(t, KindWin, "10.00")}, FailureInvalidReferenceKind},
	}
	for _, c := range cases {
		w, tx := newWallet(t, "100.00"), newTx(t, KindWin, "50.00", withRef)
		if _, err := apply(t, w, tx, c.ref); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if c.want == "" {
			if tx.Status() != StatusProcessed {
				t.Errorf("%s: estado %s", c.name, tx.Status())
			}
			continue
		}
		mustRejected(t, tx, c.want)
		mustUntouched(t, w, "100.00", 1)
	}
}

func TestApplyLoss(t *testing.T) {
	w, tx := newWallet(t, "100.00"), newTx(t, KindLoss, "0.00")
	e, err := apply(t, w, tx, Reference{})
	if err != nil || e != nil {
		t.Fatalf("LOSS não gera lançamento: %v, %v", e, err)
	}
	mustUntouched(t, w, "100.00", 1)
	if b, ok := tx.ResultBalance(); tx.Status() != StatusProcessed || !ok || b.Amount() != "100.00" {
		t.Fatalf("LOSS: %s, %v, %v", tx.Status(), b, ok)
	}
}

func TestApplyWalletMismatch(t *testing.T) {
	usd, _ := ParseMoney("100.00", "USD")
	usdWallet, _ := NewWallet(idWallet, idPlayer, usd, t0)
	tests := map[string]struct {
		w  *Wallet
		tx *WagerTransaction
	}{
		"carteira": {newWallet(t, "100.00"), newTx(t, KindBet, "1.00", func(p *ExternalParams) { p.WalletID = idTx3 })},
		"jogador":  {newWallet(t, "100.00"), newTx(t, KindBet, "1.00", func(p *ExternalParams) { p.PlayerID = idTx3 })},
		"moeda":    {usdWallet, newTx(t, KindBet, "1.00")},
	}
	for name, tt := range tests {
		before := tt.w.Balance().Amount()
		if e, err := apply(t, tt.w, tt.tx, Reference{}); err != nil || e != nil {
			t.Errorf("%s: %v, %v", name, e, err)
		}
		mustRejected(t, tt.tx, FailureWalletMismatch)
		mustUntouched(t, tt.w, before, 1)
	}
}

func TestApplyMisuse(t *testing.T) {
	w := newWallet(t, "100.00")
	if _, err := Apply(nil, newTx(t, KindBet, "1.00"), Reference{}, idEntry1, tApply); !errors.Is(err, ErrInvalidState) {
		t.Errorf("carteira nula: %v", err)
	}
	if _, err := Apply(w, nil, Reference{}, idEntry1, tApply); !errors.Is(err, ErrInvalidState) {
		t.Errorf("transação nula: %v", err)
	}
	o, _ := OpenWallet(idWallet, idPlayer, idTx3, idEntry2, mustMoney(t, "1.00"), t0)
	if _, err := Apply(w, o.Transaction, Reference{}, idEntry1, tApply); !errors.Is(err, ErrInvalidState) {
		t.Errorf("OPENING: %v", err)
	}
	tx := newTx(t, KindBet, "1.00")
	if _, err := Apply(w, tx, Reference{}, idEntry1, t0.Add(-time.Second)); !errors.Is(err, ErrInvalidState) {
		t.Errorf("horário anterior: %v", err)
	}
	mustUntouched(t, w, "100.00", 1)
}

func TestApplyTerminalTransactionNeverReapplied(t *testing.T) {
	w, tx := newWallet(t, "100.00"), newTx(t, KindBet, "10.00")
	if _, err := apply(t, w, tx, Reference{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(w, tx, Reference{}, idEntry2, tApply.Add(time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reaplicação: %v", err)
	}
	mustUntouched(t, w, "90.00", 2) // um único débito
}

// Cenário do desafio: duas apostas distintas de 80.00 sobre 100.00.
func TestApplyTwoBetsOfEightyOnHundred(t *testing.T) {
	w := newWallet(t, "100.00")
	first := newTx(t, KindBet, "80.00", func(p *ExternalParams) { p.ID = idTx1; p.ExternalID = "a" })
	second := newTx(t, KindBet, "80.00", func(p *ExternalParams) { p.ID = idTx2; p.ExternalID = "b" })

	e1, err1 := Apply(w, first, Reference{}, idEntry1, tApply)
	e2, err2 := Apply(w, second, Reference{}, idEntry2, tApply.Add(time.Second))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if e1 == nil || e2 != nil {
		t.Fatalf("esperava um único débito: %v, %v", e1, e2)
	}
	if first.Status() != StatusProcessed {
		t.Errorf("primeira: %s", first.Status())
	}
	mustRejected(t, second, FailureInsufficientFunds)
	mustUntouched(t, w, "20.00", 2)
}

// --- REFUND ---

func TestApplyRefund(t *testing.T) {
	w := newWallet(t, "975.00")
	tx := newTx(t, KindRefund, "25.00")
	ref := processedRef(t, KindBet, "25.00")
	e, err := apply(t, w, tx, Reference{Tx: ref})
	if err != nil || e == nil || e.Direction() != DirectionCredit {
		t.Fatalf("Apply: %v, %v", e, err)
	}
	mustUntouched(t, w, "1000.00", 2)
	if tx.Status() != StatusProcessed || tx.ResolvedReferenceID() != ref.ID() {
		t.Fatalf("transação %s, referência %s", tx.Status(), tx.ResolvedReferenceID())
	}
}

func TestApplyRefundRejections(t *testing.T) {
	usd, _ := ParseMoney("25.00", "USD")
	tests := map[string]struct {
		ref  *WagerTransaction
		want FailureCode
	}{
		"referência não é BET (WIN)": {processedRef(t, KindWin, "25.00"), FailureInvalidReferenceKind},
		"referência é LOSS":          {processedRef(t, KindLoss, "0.00"), FailureInvalidReferenceKind},
		"valor diferente":            {processedRef(t, KindBet, "30.00"), FailureReferenceAmountMismatch},
		"provedor diferente":         {processedRef(t, KindBet, "25.00", func(p *ExternalParams) { p.ProviderID = "provider-b" }), FailureReferenceMismatch},
		"jogador diferente":          {processedRef(t, KindBet, "25.00", func(p *ExternalParams) { p.PlayerID = idTx3 }), FailureReferenceMismatch},
		"carteira diferente":         {processedRef(t, KindBet, "25.00", func(p *ExternalParams) { p.WalletID = idTx3 }), FailureReferenceMismatch},
		"moeda diferente":            {processedRef(t, KindBet, "25.00", func(p *ExternalParams) { p.Money = usd }), FailureReferenceMismatch},
		"rodada diferente":           {processedRef(t, KindBet, "25.00", func(p *ExternalParams) { p.RoundID = "round-9" }), FailureReferenceMismatch},
	}
	for name, tt := range tests {
		w, tx := newWallet(t, "100.00"), newTx(t, KindRefund, "25.00")
		if e, err := apply(t, w, tx, Reference{Tx: tt.ref}); err != nil || e != nil {
			t.Errorf("%s: %v, %v", name, e, err)
		}
		if tx.Status() != StatusRejected || tx.FailureCode() != tt.want {
			t.Errorf("%s: %s/%s, esperado %s", name, tx.Status(), tx.FailureCode(), tt.want)
		}
		mustUntouched(t, w, "100.00", 1)
	}
}

// --- ROLLBACK ---

func TestApplyRollbackDirections(t *testing.T) {
	tests := []struct {
		name    string
		ref     *WagerTransaction
		dir     Direction
		balance string
	}{
		{"BET devolve o débito", processedRef(t, KindBet, "25.00"), DirectionCredit, "125.00"},
		{"WIN desfaz o crédito", processedRef(t, KindWin, "25.00"), DirectionDebit, "75.00"},
		{"REFUND desfaz o crédito", processedRef(t, KindRefund, "25.00", func(p *ExternalParams) { p.ReferenceExternalID = "x" }), DirectionDebit, "75.00"},
	}
	for _, tt := range tests {
		w, tx := newWallet(t, "100.00"), newTx(t, KindRollback, "25.00")
		e, err := apply(t, w, tx, Reference{Tx: tt.ref})
		if err != nil || e == nil || e.Direction() != tt.dir {
			t.Errorf("%s: %v, %v", tt.name, e, err)
			continue
		}
		mustUntouched(t, w, tt.balance, 2)
		if tx.Status() != StatusProcessed {
			t.Errorf("%s: estado %s", tt.name, tx.Status())
		}
	}
}

func TestApplyRollbackInvalidReferenceKinds(t *testing.T) {
	refs := map[string]*WagerTransaction{
		"LOSS":     processedRef(t, KindLoss, "0.00"),
		"ROLLBACK": processedRef(t, KindRollback, "25.00"),
	}
	for name, ref := range refs {
		w, tx := newWallet(t, "100.00"), newTx(t, KindRollback, "25.00")
		if _, err := apply(t, w, tx, Reference{Tx: ref}); err != nil {
			t.Fatal(err)
		}
		if tx.Status() != StatusRejected || tx.FailureCode() != FailureInvalidReferenceKind {
			t.Errorf("%s: %s/%s", name, tx.Status(), tx.FailureCode())
		}
		mustUntouched(t, w, "100.00", 1)
	}
}

func TestApplyRollbackAmountMismatch(t *testing.T) {
	w, tx := newWallet(t, "100.00"), newTx(t, KindRollback, "25.00")
	apply(t, w, tx, Reference{Tx: processedRef(t, KindBet, "30.00")})
	mustRejected(t, tx, FailureReferenceAmountMismatch)
}

func TestApplyReversalInsufficientFundsHasDistinctCode(t *testing.T) {
	w, tx := newWallet(t, "10.00"), newTx(t, KindRollback, "50.00")
	e, err := apply(t, w, tx, Reference{Tx: processedRef(t, KindWin, "50.00")})
	if err != nil || e != nil {
		t.Fatalf("%v, %v", e, err)
	}
	mustRejected(t, tx, FailureReversalInsufficientFunds)
	mustUntouched(t, w, "10.00", 1)
	if FailureReversalInsufficientFunds == FailureInsufficientFunds {
		t.Fatal("os códigos de saldo insuficiente devem ser distintos")
	}
}

// --- referência ausente, pendente ou sem sucesso ---

func TestApplyReversalWaitsForReference(t *testing.T) {
	pendingRef := newTx(t, KindBet, "25.00", func(p *ExternalParams) { p.ID = idTx2; p.ExternalID = "ref-1" })
	pendingRef2 := newTx(t, KindBet, "25.00", func(p *ExternalParams) { p.ID = idTx2; p.ExternalID = "ref-1" })
	_ = pendingRef2.MarkPendingReference(t0)
	for name, ref := range map[string]Reference{
		"ausente":           {},
		"PENDING":           {Tx: pendingRef},
		"PENDING_REFERENCE": {Tx: pendingRef2},
	} {
		for _, kind := range []Kind{KindRefund, KindRollback} {
			w, tx := newWallet(t, "100.00"), newTx(t, kind, "25.00")
			if e, err := apply(t, w, tx, ref); err != nil || e != nil {
				t.Errorf("%s/%s: %v, %v", name, kind, e, err)
			}
			if tx.Status() != StatusPendingReference || tx.FailureCode() != "" {
				t.Errorf("%s/%s: estado %s", name, kind, tx.Status())
			}
			mustUntouched(t, w, "100.00", 1)
		}
	}
}

func TestApplyReversalRetryKeepsWaitingThenResumes(t *testing.T) {
	w, tx := newWallet(t, "975.00"), newTx(t, KindRefund, "25.00")
	for range 3 {
		if _, err := apply(t, w, tx, Reference{}); err != nil {
			t.Fatal(err)
		}
		if tx.Status() != StatusPendingReference {
			t.Fatalf("estado %s", tx.Status())
		}
	}
	e, err := Apply(w, tx, Reference{Tx: processedRef(t, KindBet, "25.00")}, idEntry1, tApply.Add(time.Hour))
	if err != nil || e == nil || tx.Status() != StatusProcessed {
		t.Fatalf("retomada: %v, %v, %s", e, err, tx.Status())
	}
	mustUntouched(t, w, "1000.00", 2)
}

func TestApplyReversalReferenceWithoutSuccess(t *testing.T) {
	rejected := newTx(t, KindBet, "25.00", func(p *ExternalParams) { p.ID = idTx2 })
	_ = rejected.Reject(FailureInsufficientFunds, t0)
	failed := newTx(t, KindBet, "25.00", func(p *ExternalParams) { p.ID = idTx2 })
	_ = failed.Fail("DB_DOWN", t0)
	for name, ref := range map[string]*WagerTransaction{"REJECTED": rejected, "FAILED": failed} {
		w, tx := newWallet(t, "100.00"), newTx(t, KindRollback, "25.00")
		if e, err := apply(t, w, tx, Reference{Tx: ref}); err != nil || e != nil {
			t.Errorf("%s: %v, %v", name, e, err)
		}
		mustRejected(t, tx, FailureReferenceNotProcessed)
		mustUntouched(t, w, "100.00", 1)
	}
}

func TestRejectReferenceNotFoundAfterRetriesExhausted(t *testing.T) {
	w, tx := newWallet(t, "100.00"), newTx(t, KindRefund, "25.00")
	_, _ = apply(t, w, tx, Reference{})
	if err := tx.Reject(FailureReferenceNotFound, tApply.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mustRejected(t, tx, FailureReferenceNotFound)
}

// --- uma reversão por referência ---

func TestApplyAlreadyReversedReference(t *testing.T) {
	for _, kind := range []Kind{KindRefund, KindRollback} {
		w, tx := newWallet(t, "100.00"), newTx(t, kind, "25.00")
		if _, err := apply(t, w, tx, Reference{Tx: processedRef(t, KindBet, "25.00"), Reversed: true}); err != nil {
			t.Fatal(err)
		}
		mustRejected(t, tx, FailureReferenceAlreadyReversed)
		mustUntouched(t, w, "100.00", 1)
	}
}

// REFUND e depois ROLLBACK da mesma BET: o chamador marca a BET como já revertida.
func TestApplyRefundThenRollbackOfSameBet(t *testing.T) {
	w := newWallet(t, "975.00")
	bet := processedRef(t, KindBet, "25.00")
	refund := newTx(t, KindRefund, "25.00", func(p *ExternalParams) { p.ID = idTx1; p.ExternalID = "r1" })
	if _, err := Apply(w, refund, Reference{Tx: bet}, idEntry1, tApply); err != nil || refund.Status() != StatusProcessed {
		t.Fatalf("refund: %v, %s", err, refund.Status())
	}
	rollback := newTx(t, KindRollback, "25.00", func(p *ExternalParams) { p.ID = idTx3; p.ExternalID = "r2" })
	e, err := Apply(w, rollback, Reference{Tx: bet, Reversed: true}, idEntry2, tApply.Add(time.Second))
	if err != nil || e != nil {
		t.Fatalf("rollback: %v, %v", e, err)
	}
	mustRejected(t, rollback, FailureReferenceAlreadyReversed)
	mustUntouched(t, w, "1000.00", 2) // devolvido uma única vez
}

func TestApplyRollbackOfRefundAllowedOnce(t *testing.T) {
	w := newWallet(t, "1000.00")
	refund := processedRef(t, KindRefund, "25.00", func(p *ExternalParams) { p.ReferenceExternalID = "x" })
	first := newTx(t, KindRollback, "25.00", func(p *ExternalParams) { p.ID = idTx1; p.ExternalID = "r1" })
	e, err := Apply(w, first, Reference{Tx: refund}, idEntry1, tApply)
	if err != nil || e == nil || e.Direction() != DirectionDebit {
		t.Fatalf("primeiro rollback: %v, %v", e, err)
	}
	second := newTx(t, KindRollback, "25.00", func(p *ExternalParams) { p.ID = idTx3; p.ExternalID = "r2" })
	if _, err := Apply(w, second, Reference{Tx: refund, Reversed: true}, idEntry2, tApply.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	mustRejected(t, second, FailureReferenceAlreadyReversed)
	mustUntouched(t, w, "975.00", 2)
}
