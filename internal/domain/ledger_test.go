package domain

import (
	"errors"
	"testing"
)

func TestLedgerEntryValid(t *testing.T) {
	e, err := NewLedgerEntry(idEntry1, idWallet, idTx1, DirectionDebit,
		mustMoney(t, "25.00"), mustMoney(t, "100.00"), mustMoney(t, "75.00"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID() != idEntry1 || e.Direction() != DirectionDebit || e.BalanceAfter().Amount() != "75.00" || !e.CreatedAt().Equal(t0) {
		t.Fatalf("getters inesperados: %+v", e)
	}
	c, err := NewLedgerEntry(idEntry2, idWallet, idTx1, DirectionCredit,
		mustMoney(t, "25.00"), mustMoney(t, "0.00"), mustMoney(t, "25.00"), t0)
	if err != nil || c.Direction() != DirectionCredit {
		t.Fatalf("crédito: %v", err)
	}
}

func TestLedgerEntryInconsistent(t *testing.T) {
	m := func(a string) Money { return mustMoney(t, a) }
	usd, _ := ParseMoney("25.00", "USD")
	tests := map[string]struct {
		dir                  Direction
		money, before, after Money
	}{
		"débito com saldo errado":  {DirectionDebit, m("25.00"), m("100.00"), m("80.00")},
		"crédito com saldo errado": {DirectionCredit, m("25.00"), m("100.00"), m("75.00")},
		"direção invertida":        {DirectionCredit, m("25.00"), m("100.00"), m("75.00")},
		"direção desconhecida":     {"SIDEWAYS", m("25.00"), m("100.00"), m("125.00")},
		"valor zero":               {DirectionCredit, m("0.00"), m("100.00"), m("100.00")},
		"moeda divergente":         {DirectionCredit, usd, m("100.00"), m("125.00")},
		"saldo anterior inválido":  {DirectionCredit, m("25.00"), Money{}, m("25.00")},
		"saldo posterior negativo": {DirectionDebit, m("125.00"), m("100.00"), mustNeg(t, m("25.00"))},
	}
	for name, tt := range tests {
		_, err := NewLedgerEntry(idEntry1, idWallet, idTx1, tt.dir, tt.money, tt.before, tt.after, t0)
		if err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
	// o erro de aritmética é classificável
	_, err := NewLedgerEntry(idEntry1, idWallet, idTx1, DirectionDebit, m("25.00"), m("100.00"), m("80.00"), t0)
	if !errors.Is(err, ErrInconsistentEntry) {
		t.Errorf("erro %v não é ErrInconsistentEntry", err)
	}
}

func TestLedgerEntryInvalidIDsAndTime(t *testing.T) {
	m := mustMoney(t, "1.00")
	z := mustMoney(t, "0.00")
	if _, err := NewLedgerEntry("", idWallet, idTx1, DirectionCredit, m, z, m, t0); !errors.Is(err, ErrInvalidID) {
		t.Errorf("id vazio: %v", err)
	}
	if _, err := NewLedgerEntry(idEntry1, "x", idTx1, DirectionCredit, m, z, m, t0); !errors.Is(err, ErrInvalidID) {
		t.Errorf("walletId inválido: %v", err)
	}
	if _, err := NewLedgerEntry(idEntry1, idWallet, idTx1, DirectionCredit, m, z, m, time0()); !errors.Is(err, ErrInvalidState) {
		t.Errorf("createdAt zero: %v", err)
	}
}

func TestRehydrateLedgerEntryValidates(t *testing.T) {
	if _, err := RehydrateLedgerEntry(idEntry1, idWallet, idTx1, DirectionDebit,
		mustMoney(t, "25.00"), mustMoney(t, "100.00"), mustMoney(t, "50.00"), t0); err == nil {
		t.Fatal("dados persistidos inconsistentes devem ser rejeitados")
	}
}
