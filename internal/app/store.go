package app

import (
	"context"
	"time"

	"desafio/internal/domain"
)

// Store é a unidade de trabalho sobre o armazenamento persistente.
type Store interface {
	// InTx executa fn em uma transação; qualquer erro faz rollback e o sucesso confirma.
	InTx(ctx context.Context, fn func(ctx context.Context, r Repos) error) error
	// Read executa fn sobre uma visão consistente e somente leitura dos dados.
	Read(ctx context.Context, fn func(ctx context.Context, r Repos) error) error
}

// Repos agrupa os repositórios de uma mesma transação ou visão.
type Repos interface {
	Wallets() WalletRepository
	Transactions() TransactionRepository
	Ledger() LedgerRepository
	PendingReferences() PendingReferenceRepository
}

type WalletRepository interface {
	// Insert devolve ErrConflict se o jogador já tem carteira na moeda.
	Insert(ctx context.Context, w *domain.Wallet) error
	// Get e GetForUpdate devolvem ErrNotFound; GetForUpdate trava a carteira até o fim da transação.
	Get(ctx context.Context, id domain.ID) (*domain.Wallet, error)
	GetForUpdate(ctx context.Context, id domain.ID) (*domain.Wallet, error)
	// Update grava saldo, versão e updatedAt se a versão persistida for expectedVersion; senão ErrStaleWallet.
	Update(ctx context.Context, w *domain.Wallet, expectedVersion int64) error
}

type TransactionRepository interface {
	Insert(ctx context.Context, tx *domain.WagerTransaction) error
	// GetByID e GetByExternal devolvem ErrNotFound.
	GetByID(ctx context.Context, id domain.ID) (*domain.WagerTransaction, error)
	GetByExternal(ctx context.Context, providerID, externalTransactionID string) (*domain.WagerTransaction, error)
	// HasProcessedReversal informa se a transação já recebeu um REFUND ou ROLLBACK PROCESSED.
	HasProcessedReversal(ctx context.Context, referenceID domain.ID) (bool, error)
	// UpdateOutcome grava o desfecho (PROCESSED, REJECTED ou FAILED) de uma transação que estava em
	// PENDING_REFERENCE e encerra seu agendamento; se ela já não estiver pendente, devolve ErrStaleTransaction.
	UpdateOutcome(ctx context.Context, tx *domain.WagerTransaction) error
}

// PendingReference identifica uma reversão em PENDING_REFERENCE reivindicada por um worker.
type PendingReference struct {
	ID       domain.ID
	WalletID domain.ID
	Attempts int
}

type PendingReferenceRepository interface {
	// Claim reivindica até limit pendências com próxima tentativa <= now, ordenadas por vencimento, sem
	// bloquear as reivindicadas por outras instâncias (SKIP LOCKED), e adia o agendamento delas para until:
	// se o worker cair, elas voltam a ser reivindicáveis depois da lease.
	Claim(ctx context.Context, now, until time.Time, limit int) ([]PendingReference, error)
	// Reschedule registra o número de tentativas e a próxima tentativa de uma pendência ainda aberta.
	Reschedule(ctx context.Context, id domain.ID, attempts int, next time.Time) error
}

// LedgerRow é um lançamento com sua posição estável na paginação.
type LedgerRow struct {
	Seq   int64
	Entry *domain.WalletLedgerEntry
}

// LedgerTotals soma créditos e débitos de uma carteira e conta os lançamentos.
type LedgerTotals struct {
	Credits domain.Money
	Debits  domain.Money
	Count   int
}

type LedgerRepository interface {
	Insert(ctx context.Context, e *domain.WalletLedgerEntry) error
	// List devolve até limit lançamentos com seq > afterSeq, em ordem crescente de seq.
	List(ctx context.Context, walletID domain.ID, afterSeq int64, limit int) ([]LedgerRow, error)
	Totals(ctx context.Context, walletID domain.ID, currency string) (LedgerTotals, error)
}

// IDGenerator e Clock entram por parâmetro para manter os casos de uso determinísticos em teste.
type IDGenerator interface{ NewID() domain.ID }

type Clock interface{ Now() time.Time }
