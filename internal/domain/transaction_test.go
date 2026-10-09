package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewExternalTransaction(t *testing.T) {
	tx := newTx(t, KindBet, "25.00")
	if tx.Status() != StatusPending || tx.FailureCode() != "" || tx.Kind() != KindBet || tx.ProviderID() != "provider-a" ||
		tx.Money().Amount() != "25.00" || tx.ID() != idTx1 || !tx.CreatedAt().Equal(t0) {
		t.Fatalf("transação inesperada: %+v", tx)
	}
	if _, ok := tx.ResultBalance(); ok {
		t.Error("transação pendente não tem saldo observado")
	}
}

func TestNewExternalTransactionKindAndAmountPolicy(t *testing.T) {
	for _, k := range []Kind{KindBet, KindWin, KindRefund, KindRollback} {
		p := extParams(t, k, "0.00")
		if _, err := NewExternalTransaction(p); !errors.Is(err, ErrInvalidMoney) {
			t.Errorf("%s com valor zero: %v", k, err)
		}
		if _, err := NewExternalTransaction(extParams(t, k, "1.00")); err != nil {
			t.Errorf("%s com valor positivo: %v", k, err)
		}
	}
	if _, err := NewExternalTransaction(extParams(t, KindLoss, "1.00")); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("LOSS com valor: %v", err)
	}
	if _, err := NewExternalTransaction(extParams(t, KindLoss, "0.00")); err != nil {
		t.Errorf("LOSS com zero: %v", err)
	}
	for _, k := range []Kind{KindOpening, "BOGUS", ""} {
		if _, err := NewExternalTransaction(extParams(t, k, "1.00")); !errors.Is(err, ErrInvalidState) {
			t.Errorf("tipo %q: %v", k, err)
		}
	}
}

func TestNewExternalTransactionRequiredFields(t *testing.T) {
	mutations := map[string]func(*ExternalParams){
		"provedor":   func(p *ExternalParams) { p.ProviderID = "" },
		"id externo": func(p *ExternalParams) { p.ExternalID = "" },
		"chave":      func(p *ExternalParams) { p.IdempotencyKey = "" },
		"hash":       func(p *ExternalParams) { p.RequestHash = "" },
		"rodada":     func(p *ExternalParams) { p.RoundID = "" },
		"jogo":       func(p *ExternalParams) { p.GameID = "" },
		"id":         func(p *ExternalParams) { p.ID = "" },
		"carteira":   func(p *ExternalParams) { p.WalletID = "x" },
		"jogador":    func(p *ExternalParams) { p.PlayerID = "" },
		"valor":      func(p *ExternalParams) { p.Money = Money{} },
		"horário":    func(p *ExternalParams) { p.Now = time.Time{} },
		"referência": func(p *ExternalParams) { p.Kind = KindRefund; p.ReferenceExternalID = "" },
	}
	for name, m := range mutations {
		if _, err := NewExternalTransaction(extParams(t, KindBet, "1.00", m)); err == nil {
			t.Errorf("%s ausente deveria falhar", name)
		}
	}
}

func TestTransitionTable(t *testing.T) {
	allowed := map[Status]map[Status]bool{
		StatusPending:          {StatusPendingReference: true, StatusProcessed: true, StatusRejected: true, StatusFailed: true},
		StatusPendingReference: {StatusProcessed: true, StatusRejected: true, StatusFailed: true},
	}
	statuses := []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}
	bal := mustMoney(t, "10.00")
	// leva uma transação nova ao estado `from`
	at := func(from Status) *WagerTransaction {
		tx := newTx(t, KindBet, "1.00")
		later := t0.Add(time.Minute)
		switch from {
		case StatusPendingReference:
			_ = tx.MarkPendingReference(later)
		case StatusProcessed:
			_ = tx.MarkProcessed(bal, later)
		case StatusRejected:
			_ = tx.Reject("X", later)
		case StatusFailed:
			_ = tx.Fail("X", later)
		}
		if tx.Status() != from {
			t.Fatalf("não chegou a %s: %s", from, tx.Status())
		}
		return tx
	}
	now := t0.Add(time.Hour)
	for _, from := range statuses {
		for _, to := range statuses {
			tx := at(from)
			var err error
			switch to {
			case StatusPending:
				err = tx.transition(StatusPending, now) // nenhum estado volta a PENDING
			case StatusPendingReference:
				err = tx.MarkPendingReference(now)
			case StatusProcessed:
				err = tx.MarkProcessed(bal, now)
			case StatusRejected:
				err = tx.Reject("X", now)
			case StatusFailed:
				err = tx.Fail("X", now)
			}
			if allowed[from][to] {
				if err != nil || tx.Status() != to || !tx.UpdatedAt().Equal(now) {
					t.Errorf("%s → %s deveria ser válida: %v", from, to, err)
				}
				continue
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s → %s: erro %v, esperado ErrInvalidTransition", from, to, err)
			}
			if tx.Status() != from {
				t.Errorf("%s → %s alterou o estado para %s", from, to, tx.Status())
			}
		}
	}
}

func TestTransitionDetails(t *testing.T) {
	tx := newTx(t, KindBet, "1.00")
	if err := tx.Reject("", t0); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Reject sem código: %v", err)
	}
	if err := tx.Fail("", t0); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Fail sem código: %v", err)
	}
	if err := tx.MarkProcessed(Money{}, t0); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("MarkProcessed sem saldo: %v", err)
	}
	if tx.Status() != StatusPending {
		t.Fatalf("estado mudou após falhas: %s", tx.Status())
	}
	if err := tx.Reject("INSUFFICIENT_FUNDS", t0.Add(-time.Second)); !errors.Is(err, ErrInvalidState) {
		t.Errorf("instante anterior ao último: %v", err)
	}
	if err := tx.Reject("INSUFFICIENT_FUNDS", t0.Add(time.Second)); err != nil || tx.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Errorf("Reject: %v", err)
	}
	if _, ok := tx.ResultBalance(); ok {
		t.Error("rejeitada não tem saldo observado")
	}
	tx2 := newTx(t, KindBet, "1.00")
	if err := tx2.MarkProcessed(mustMoney(t, "9.00"), t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if b, ok := tx2.ResultBalance(); !ok || b.Amount() != "9.00" {
		t.Errorf("saldo observado = %v, %v", b, ok)
	}
}

func snapshotOf(t *testing.T, status Status, mutate ...func(*TransactionSnapshot)) TransactionSnapshot {
	t.Helper()
	s := TransactionSnapshot{
		ID: idTx1, ProviderID: "provider-a", ExternalID: "ext-1", IdempotencyKey: "k", RequestHash: "h",
		WalletID: idWallet, PlayerID: idPlayer, RoundID: "r", GameID: "g", Kind: KindBet,
		Money: mustMoney(t, "25.00"), Status: status, CreatedAt: t0, UpdatedAt: t0,
	}
	switch status {
	case StatusRejected, StatusFailed:
		s.FailureCode = "INSUFFICIENT_FUNDS"
	case StatusProcessed:
		s.ResultBalance = mustMoney(t, "75.00")
	}
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func TestRehydrateTransactionConsistent(t *testing.T) {
	for _, st := range []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed} {
		tx, err := RehydrateTransaction(snapshotOf(t, st))
		if err != nil {
			t.Errorf("%s: %v", st, err)
			continue
		}
		if tx.Status() != st {
			t.Errorf("estado alterado: %s → %s", st, tx.Status())
		}
	}
	tx, _ := RehydrateTransaction(snapshotOf(t, StatusRejected))
	if tx.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q", tx.FailureCode())
	}
}

func TestRehydrateTransactionInconsistent(t *testing.T) {
	cases := map[string]TransactionSnapshot{
		"REJECTED sem código":      snapshotOf(t, StatusRejected, func(s *TransactionSnapshot) { s.FailureCode = "" }),
		"FAILED sem código":        snapshotOf(t, StatusFailed, func(s *TransactionSnapshot) { s.FailureCode = "" }),
		"PROCESSED com código":     snapshotOf(t, StatusProcessed, func(s *TransactionSnapshot) { s.FailureCode = "X" }),
		"PROCESSED sem saldo":      snapshotOf(t, StatusProcessed, func(s *TransactionSnapshot) { s.ResultBalance = Money{} }),
		"PENDING com código":       snapshotOf(t, StatusPending, func(s *TransactionSnapshot) { s.FailureCode = "X" }),
		"PENDING com saldo":        snapshotOf(t, StatusPending, func(s *TransactionSnapshot) { s.ResultBalance = mustMoney(t, "1.00") }),
		"REJECTED com saldo":       snapshotOf(t, StatusRejected, func(s *TransactionSnapshot) { s.ResultBalance = mustMoney(t, "1.00") }),
		"estado desconhecido":      snapshotOf(t, "BOGUS"),
		"campo obrigatório":        snapshotOf(t, StatusPending, func(s *TransactionSnapshot) { s.RoundID = "" }),
		"tipo OPENING sem opening": snapshotOf(t, StatusProcessed, func(s *TransactionSnapshot) { s.Kind = KindOpening }),
	}
	for name, s := range cases {
		if _, err := RehydrateTransaction(s); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func TestRehydrateOpeningTransaction(t *testing.T) {
	s := TransactionSnapshot{
		ID: idTx1, WalletID: idWallet, PlayerID: idPlayer, Kind: KindOpening, Money: mustMoney(t, "1000.00"),
		Status: StatusProcessed, ResultBalance: mustMoney(t, "1000.00"), CreatedAt: t0, UpdatedAt: t0,
	}
	if _, err := RehydrateTransaction(s); err != nil {
		t.Fatal(err)
	}
	s.ProviderID = "p"
	if _, err := RehydrateTransaction(s); err == nil {
		t.Error("OPENING com metadados externos deveria falhar")
	}
	s.ProviderID = ""
	s.Status = StatusPending
	s.ResultBalance = Money{}
	if _, err := RehydrateTransaction(s); err == nil {
		t.Error("OPENING fora de PROCESSED deveria falhar")
	}
}
