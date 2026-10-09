package app

import (
	"errors"
	"testing"
	"time"

	"desafio/internal/domain"
)

const (
	cWallet = "0192f291-27dd-7d3f-8071-5f8685deef37"
	cPlayer = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"
	cTx     = "0192f298-345e-7e38-af88-e43f851a819d"
)

func TestToDomainMoney(t *testing.T) {
	m, err := toDomainMoney(Money{Amount: "25.00", Currency: "BRL"})
	if err != nil || m.Amount() != "25.00" || m.Currency() != "BRL" {
		t.Fatalf("válido: %v, %v", m, err)
	}
	for _, in := range []Money{{"10", "BRL"}, {"1.00", "brl"}, {"-1.00", "BRL"}, {"", ""}} {
		if _, err := toDomainMoney(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: erro %v, esperado ErrInvalidInput", in, err)
		}
	}
}

func TestToDomainID(t *testing.T) {
	if _, err := toDomainID(cWallet); err != nil {
		t.Fatal(err)
	}
	if _, err := toDomainID("abc"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("id inválido: %v", err)
	}
}

func TestInvalidInputMapping(t *testing.T) {
	for _, e := range []error{
		domain.ErrInvalidMoney, domain.ErrInvalidCurrency, domain.ErrCurrencyMismatch,
		domain.ErrOverflow, domain.ErrInvalidID, domain.ErrInvalidState,
	} {
		if err := invalidInput(e); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%v não virou ErrInvalidInput", e)
		}
	}
	// erros que não são de validação passam intactos (saldo insuficiente, transição inválida etc.)
	for _, e := range []error{domain.ErrInsufficientFunds, domain.ErrInvalidTransition, ErrUnavailable} {
		if err := invalidInput(e); errors.Is(err, ErrInvalidInput) {
			t.Errorf("%v não deveria virar ErrInvalidInput", e)
		}
	}
}

func TestBET_ZeroAmountIsInvalidInput(t *testing.T) {
	zero, _ := domain.ParseMoney("0.00", "BRL")
	_, err := domain.NewExternalTransaction(domain.ExternalParams{
		ID: cTx, ProviderID: "p", ExternalID: "e", IdempotencyKey: "k", RequestHash: "h",
		WalletID: cWallet, PlayerID: cPlayer, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: zero, Now: time.Now(),
	})
	if err := invalidInput(err); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("BET com zero: %v", err)
	}
}

func TestDTOConversions(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	bal, _ := domain.ParseMoney("975.00", "BRL")
	w, err := domain.NewWallet(cWallet, cPlayer, bal, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := walletToDTO(w); got.ID != cWallet || got.Balance.Amount != "975.00" || got.Version != 1 {
		t.Errorf("wallet: %+v", got)
	}

	amt, _ := domain.ParseMoney("25.00", "BRL")
	tx, _ := domain.NewExternalTransaction(domain.ExternalParams{
		ID: cTx, ProviderID: "p", ExternalID: "e", IdempotencyKey: "k", RequestHash: "h",
		WalletID: cWallet, PlayerID: cPlayer, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: amt, Now: now,
	})
	if res := resultOf(tx, false); res.Status != "PENDING" || res.Balance != nil || res.IdempotentReplay {
		t.Errorf("pendente: %+v", res)
	}
	_ = tx.MarkProcessed(bal, now)
	if res := resultOf(tx, true); res.Status != "PROCESSED" || res.Balance == nil || res.Balance.Amount != "975.00" || !res.IdempotentReplay {
		t.Errorf("processada: %+v", res)
	}
	if dto := transactionToDTO(tx); dto.ExternalTransactionID != "e" || dto.Kind != "BET" || dto.Money.Amount != "25.00" {
		t.Errorf("transação: %+v", dto)
	}

	e, _ := domain.NewLedgerEntry(cTx, cWallet, cTx, domain.DirectionDebit, amt, mustM("1000.00"), bal, now)
	if dto := ledgerEntryToDTO(e); dto.Direction != "DEBIT" || dto.BalanceBefore.Amount != "1000.00" || dto.BalanceAfter.Amount != "975.00" {
		t.Errorf("lançamento: %+v", dto)
	}
}

func mustM(a string) domain.Money {
	m, err := domain.ParseMoney(a, "BRL")
	if err != nil {
		panic(err)
	}
	return m
}
