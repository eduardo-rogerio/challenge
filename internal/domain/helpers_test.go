package domain

import (
	"testing"
	"time"
)

// Identificadores e relógio fixos para testes determinísticos.
const (
	idWallet = ID("0192f291-27dd-7d3f-8071-5f8685deef37")
	idPlayer = ID("0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1")
	idTx1    = ID("0192f298-345e-7e38-af88-e43f851a819d")
	idTx2    = ID("0192f298-345e-7e38-af88-e43f851a819e")
	idTx3    = ID("0192f298-345e-7e38-af88-e43f851a819f")
	idEntry1 = ID("0192f2a0-0000-7000-8000-000000000001")
	idEntry2 = ID("0192f2a0-0000-7000-8000-000000000002")
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func mustMoney(t *testing.T, amount string) Money {
	t.Helper()
	m, err := ParseMoney(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustNeg(t *testing.T, m Money) Money {
	t.Helper()
	n, err := m.Neg()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func time0() (z time.Time) { return }

// extParams devolve parâmetros válidos de uma operação externa; mutate ajusta o caso do teste.
func extParams(t *testing.T, kind Kind, amount string, mutate ...func(*ExternalParams)) ExternalParams {
	t.Helper()
	p := ExternalParams{
		ID: idTx1, ProviderID: "provider-a", ExternalID: "ext-1", IdempotencyKey: "provider-a:ext-1",
		RequestHash: "hash", WalletID: idWallet, PlayerID: idPlayer, RoundID: "round-1", GameID: "game-1",
		Kind: kind, Money: mustMoney(t, amount), Now: t0,
	}
	if kind.reversal() {
		p.ReferenceExternalID = "ref-1"
	}
	for _, m := range mutate {
		m(&p)
	}
	return p
}

func newTx(t *testing.T, kind Kind, amount string, mutate ...func(*ExternalParams)) *WagerTransaction {
	t.Helper()
	tx, err := NewExternalTransaction(extParams(t, kind, amount, mutate...))
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
