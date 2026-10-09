package domain

import (
	"fmt"
	"time"
)

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

// WalletLedgerEntry é um lançamento imutável: só expõe getters.
type WalletLedgerEntry struct {
	id            ID
	walletID      ID
	transactionID ID
	direction     Direction
	money         Money
	balanceBefore Money
	balanceAfter  Money
	createdAt     time.Time
}

// NewLedgerEntry valida os campos e a aritmética balanceAfter = balanceBefore ± money.
func NewLedgerEntry(id, walletID, transactionID ID, dir Direction, money, before, after Money, createdAt time.Time) (*WalletLedgerEntry, error) {
	for _, v := range []ID{id, walletID, transactionID} {
		if !v.valid() {
			return nil, fmt.Errorf("%w: %q", ErrInvalidID, v)
		}
	}
	if createdAt.IsZero() {
		return nil, fmt.Errorf("%w: createdAt ausente", ErrInvalidState)
	}
	if !money.IsPositive() {
		return nil, fmt.Errorf("%w: valor do lançamento deve ser positivo", ErrInconsistentEntry)
	}
	if before.IsNegative() || after.IsNegative() {
		return nil, fmt.Errorf("%w: saldo negativo", ErrInconsistentEntry)
	}
	var expected Money
	var err error
	switch dir {
	case DirectionCredit:
		expected, err = before.Add(money)
	case DirectionDebit:
		expected, err = before.Sub(money)
	default:
		return nil, fmt.Errorf("%w: direção %q", ErrInconsistentEntry, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInconsistentEntry, err)
	}
	if !expected.Equal(after) {
		return nil, fmt.Errorf("%w: saldo posterior %s, esperado %s", ErrInconsistentEntry, after, expected)
	}
	return &WalletLedgerEntry{id, walletID, transactionID, dir, money, before, after, createdAt}, nil
}

// RehydrateLedgerEntry reconstrói um lançamento persistido, com as mesmas validações da criação.
func RehydrateLedgerEntry(id, walletID, transactionID ID, dir Direction, money, before, after Money, createdAt time.Time) (*WalletLedgerEntry, error) {
	return NewLedgerEntry(id, walletID, transactionID, dir, money, before, after, createdAt)
}

func (e *WalletLedgerEntry) ID() ID               { return e.id }
func (e *WalletLedgerEntry) WalletID() ID         { return e.walletID }
func (e *WalletLedgerEntry) TransactionID() ID    { return e.transactionID }
func (e *WalletLedgerEntry) Direction() Direction { return e.direction }
func (e *WalletLedgerEntry) Money() Money         { return e.money }
func (e *WalletLedgerEntry) BalanceBefore() Money { return e.balanceBefore }
func (e *WalletLedgerEntry) BalanceAfter() Money  { return e.balanceAfter }
func (e *WalletLedgerEntry) CreatedAt() time.Time { return e.createdAt }
