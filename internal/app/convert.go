package app

import (
	"errors"
	"fmt"

	"desafio/internal/domain"
)

// Conversões entre os DTOs em string da API e os tipos do domínio.

func toDomainMoney(m Money) (domain.Money, error) {
	dm, err := domain.ParseMoney(m.Amount, m.Currency)
	if err != nil {
		return domain.Money{}, invalidInput(err)
	}
	return dm, nil
}

func fromDomainMoney(m domain.Money) Money {
	return Money{Amount: m.Amount(), Currency: m.Currency()}
}

func toDomainID(s string) (domain.ID, error) {
	id, err := domain.ParseID(s)
	if err != nil {
		return "", invalidInput(err)
	}
	return id, nil
}

// invalidInput converte erros de validação do domínio em ErrInvalidInput (HTTP 400);
// qualquer outro erro é devolvido como está.
func invalidInput(err error) error {
	for _, target := range []error{
		domain.ErrInvalidMoney, domain.ErrInvalidCurrency, domain.ErrCurrencyMismatch,
		domain.ErrOverflow, domain.ErrInvalidID, domain.ErrInvalidState,
	} {
		if errors.Is(err, target) {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	}
	return err
}

func walletToDTO(w *domain.Wallet) Wallet {
	return Wallet{
		ID:       w.ID().String(),
		PlayerID: w.PlayerID().String(),
		Balance:  fromDomainMoney(w.Balance()),
		Version:  w.Version(),
	}
}

func ledgerEntryToDTO(e *domain.WalletLedgerEntry) LedgerEntry {
	return LedgerEntry{
		ID:            e.ID().String(),
		TransactionID: e.TransactionID().String(),
		Direction:     string(e.Direction()),
		Money:         fromDomainMoney(e.Money()),
		BalanceBefore: fromDomainMoney(e.BalanceBefore()),
		BalanceAfter:  fromDomainMoney(e.BalanceAfter()),
		CreatedAt:     e.CreatedAt(),
	}
}

func transactionToDTO(tx *domain.WagerTransaction) Transaction {
	return Transaction{
		ID:                    tx.ID().String(),
		ProviderID:            tx.ProviderID(),
		ExternalTransactionID: tx.ExternalID(),
		WalletID:              tx.WalletID().String(),
		Kind:                  string(tx.Kind()),
		Money:                 fromDomainMoney(tx.Money()),
		Status:                string(tx.Status()),
		FailureCode:           string(tx.FailureCode()),
	}
}

// resultOf descreve o desfecho de uma transação, com o saldo observado ao processá-la.
func resultOf(tx *domain.WagerTransaction, replay bool) TransactionResult {
	res := TransactionResult{
		TransactionID:    tx.ID().String(),
		Status:           string(tx.Status()),
		FailureCode:      string(tx.FailureCode()),
		IdempotentReplay: replay,
	}
	if b, ok := tx.ResultBalance(); ok {
		m := fromDomainMoney(b)
		res.Balance = &m
	}
	return res
}
