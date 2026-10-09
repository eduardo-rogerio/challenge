// Package app define os casos de uso expostos como portas (interfaces) e os tipos que
// trafegam entre os adapters e o domínio. Valores monetários são sempre strings decimais.
package app

import (
	"context"
	"errors"
	"time"
)

// Erros sinalizados pelos casos de uso; os adapters os traduzem para o protocolo.
var (
	ErrNotFound       = errors.New("não encontrado")
	ErrConflict       = errors.New("conflito")
	ErrUnavailable    = errors.New("indisponível")
	ErrNotImplemented = errors.New("não implementado")

	// ErrInvalidInput indica entrada semanticamente inválida (por exemplo, BET com valor zero).
	ErrInvalidInput = errors.New("entrada inválida")
	// ErrStaleWallet indica que a carteira mudou entre a leitura e a gravação (violação do lock; é um defeito).
	ErrStaleWallet = errors.New("carteira alterada por outra operação")
	// ErrStaleTransaction indica que a transação já não está em PENDING_REFERENCE (foi resolvida por outro caminho).
	ErrStaleTransaction = errors.New("transação já resolvida")

	// Idempotência: conteúdo divergente para a mesma chave ou operação externa repetida com outra chave.
	ErrIdempotencyConflict = errors.New("conflito de idempotência")
	// Outra requisição com a mesma chave ainda está em execução.
	ErrInProgress = errors.New("requisição em andamento")
)

// Estados de uma transação.
const (
	StatusPending          = "PENDING"
	StatusPendingReference = "PENDING_REFERENCE"
	StatusProcessed        = "PROCESSED"
	StatusRejected         = "REJECTED"
	StatusFailed           = "FAILED"
)

type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type Wallet struct {
	ID       string `json:"id"`
	PlayerID string `json:"playerId"`
	Balance  Money  `json:"balance"`
	Version  int64  `json:"version"`
}

type OpenWalletInput struct {
	PlayerID       string `json:"playerId"`
	InitialBalance Money  `json:"initialBalance"`
}

type LedgerEntry struct {
	ID            string    `json:"id"`
	TransactionID string    `json:"transactionId"`
	Direction     string    `json:"direction"`
	Money         Money     `json:"money"`
	BalanceBefore Money     `json:"balanceBefore"`
	BalanceAfter  Money     `json:"balanceAfter"`
	CreatedAt     time.Time `json:"createdAt"`
}

type LedgerPage struct {
	Entries    []LedgerEntry `json:"entries"`
	NextCursor *string       `json:"nextCursor"`
}

type Reconciliation struct {
	WalletID          string `json:"walletId"`
	StoredBalance     Money  `json:"storedBalance"`
	CalculatedBalance Money  `json:"calculatedBalance"`
	Difference        Money  `json:"difference"`
	Consistent        bool   `json:"consistent"`
	CheckedEntries    int    `json:"checkedEntries"`
}

type SubmitInput struct {
	ProviderID                     string `json:"providerId"`
	ExternalTransactionID          string `json:"externalTransactionId"`
	PlayerID                       string `json:"playerId"`
	WalletID                       string `json:"walletId"`
	RoundID                        string `json:"roundId"`
	GameID                         string `json:"gameId"`
	Kind                           string `json:"kind"`
	Money                          Money  `json:"money"`
	ReferenceExternalTransactionID string `json:"referenceExternalTransactionId,omitempty"`

	// IdempotencyKey vem do transporte (header HTTP) e não faz parte do corpo nem do hash.
	IdempotencyKey string `json:"-"`
}

type TransactionResult struct {
	TransactionID    string `json:"transactionId"`
	Status           string `json:"status"`
	Balance          *Money `json:"balance,omitempty"`
	FailureCode      string `json:"failureCode,omitempty"`
	IdempotentReplay bool   `json:"idempotentReplay"`
}

type Transaction struct {
	ID                    string `json:"id"`
	ProviderID            string `json:"providerId"`
	ExternalTransactionID string `json:"externalTransactionId"`
	WalletID              string `json:"walletId"`
	Kind                  string `json:"kind"`
	Money                 Money  `json:"money"`
	Status                string `json:"status"`
	FailureCode           string `json:"failureCode,omitempty"`
}

type Wallets interface {
	Open(ctx context.Context, in OpenWalletInput) (Wallet, error)
	Get(ctx context.Context, walletID string) (Wallet, error)
	Ledger(ctx context.Context, walletID, cursor string, limit int) (LedgerPage, error)
	Reconcile(ctx context.Context, walletID string) (Reconciliation, error)
}

type Transactions interface {
	Submit(ctx context.Context, in SubmitInput) (TransactionResult, error)
	GetByID(ctx context.Context, transactionID string) (Transaction, error)
	GetByExternal(ctx context.Context, providerID, externalTransactionID string) (Transaction, error)
}
