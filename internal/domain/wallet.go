package domain

import (
	"fmt"
	"time"
)

// Wallet é a raiz do agregado financeiro. A moeda é a do saldo; o saldo nunca fica negativo
// e só muda por Credit e Debit, que também incrementam a versão.
type Wallet struct {
	id        ID
	playerID  ID
	balance   Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// NewWallet cria uma carteira com versão inicial 1.
func NewWallet(id, playerID ID, balance Money, now time.Time) (*Wallet, error) {
	return RehydrateWallet(id, playerID, balance, 1, now, now)
}

// RehydrateWallet reconstrói uma carteira persistida validando as invariantes, sem aplicar movimentações.
func RehydrateWallet(id, playerID ID, balance Money, version int64, createdAt, updatedAt time.Time) (*Wallet, error) {
	switch {
	case !id.valid():
		return nil, fmt.Errorf("%w: carteira %q", ErrInvalidID, id)
	case !playerID.valid():
		return nil, fmt.Errorf("%w: jogador %q", ErrInvalidID, playerID)
	case !balance.IsValid():
		return nil, fmt.Errorf("%w: saldo não inicializado", ErrInvalidMoney)
	case balance.IsNegative():
		return nil, fmt.Errorf("%w: saldo negativo", ErrInvalidState)
	case version < 1:
		return nil, fmt.Errorf("%w: versão %d", ErrInvalidState, version)
	case createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt):
		return nil, fmt.Errorf("%w: timestamps inválidos", ErrInvalidState)
	}
	return &Wallet{id, playerID, balance, version, createdAt, updatedAt}, nil
}

func (w *Wallet) ID() ID               { return w.id }
func (w *Wallet) PlayerID() ID         { return w.playerID }
func (w *Wallet) Balance() Money       { return w.balance }
func (w *Wallet) Currency() string     { return w.balance.Currency() }
func (w *Wallet) Version() int64       { return w.version }
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

// Credit soma um valor positivo ao saldo e devolve o lançamento correspondente.
func (w *Wallet) Credit(entryID, transactionID ID, m Money, now time.Time) (*WalletLedgerEntry, error) {
	return w.move(DirectionCredit, entryID, transactionID, m, now)
}

// Debit subtrai um valor positivo do saldo; falha com ErrInsufficientFunds se exceder o saldo.
func (w *Wallet) Debit(entryID, transactionID ID, m Money, now time.Time) (*WalletLedgerEntry, error) {
	return w.move(DirectionDebit, entryID, transactionID, m, now)
}

// move constrói o lançamento antes de alterar a carteira: se algo falhar, nada muda.
func (w *Wallet) move(dir Direction, entryID, transactionID ID, m Money, now time.Time) (*WalletLedgerEntry, error) {
	if now.IsZero() || now.Before(w.updatedAt) {
		return nil, fmt.Errorf("%w: instante anterior à última atualização", ErrInvalidState)
	}
	if err := compatible(w.balance, m); err != nil {
		return nil, err
	}
	if !m.IsPositive() {
		return nil, fmt.Errorf("%w: o valor da movimentação deve ser positivo", ErrInvalidMoney)
	}
	var after Money
	var err error
	if dir == DirectionCredit {
		after, err = w.balance.Add(m)
	} else {
		if c, _ := w.balance.Cmp(m); c < 0 {
			return nil, ErrInsufficientFunds
		}
		after, err = w.balance.Sub(m)
	}
	if err != nil {
		return nil, err
	}
	entry, err := NewLedgerEntry(entryID, w.id, transactionID, dir, m, w.balance, after, now)
	if err != nil {
		return nil, err
	}
	w.balance = after
	w.version++
	w.updatedAt = now
	return entry, nil
}

// Opening é o resultado da abertura de uma carteira. Transaction e Entry são nulos
// quando o saldo inicial é zero.
type Opening struct {
	Wallet      *Wallet
	Transaction *WagerTransaction
	Entry       *WalletLedgerEntry
}

// OpenWallet abre uma carteira com versão 1. Com saldo inicial positivo, também cria a transação
// interna OPENING (já PROCESSED) e o lançamento de crédito de saldo anterior zero; com saldo zero,
// só a carteira. Saldo inicial negativo é rejeitado.
func OpenWallet(walletID, playerID, transactionID, entryID ID, initial Money, now time.Time) (*Opening, error) {
	w, err := NewWallet(walletID, playerID, initial, now)
	if err != nil {
		return nil, err
	}
	if initial.IsZero() {
		return &Opening{Wallet: w}, nil
	}
	zero, err := ZeroMoney(initial.Currency())
	if err != nil {
		return nil, err
	}
	entry, err := NewLedgerEntry(entryID, walletID, transactionID, DirectionCredit, initial, zero, initial, now)
	if err != nil {
		return nil, err
	}
	tx, err := RehydrateTransaction(TransactionSnapshot{
		ID: transactionID, WalletID: walletID, PlayerID: playerID, Kind: KindOpening, Money: initial,
		Status: StatusProcessed, ResultBalance: initial, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	return &Opening{Wallet: w, Transaction: tx, Entry: entry}, nil
}
