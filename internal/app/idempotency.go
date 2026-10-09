package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// IdempotencyStore persiste o estado de cada chave de idempotência.
type IdempotencyStore interface {
	// Begin registra a chave. Devolve (nil, nil) quando o chamador deve executar a operação;
	// o resultado salvo quando é um replay; ErrInProgress ou ErrIdempotencyConflict nos demais casos.
	Begin(ctx context.Context, key, providerID, externalTransactionID, requestHash string) (*TransactionResult, error)
	// Complete grava o resultado e conclui a chave.
	Complete(ctx context.Context, key string, result TransactionResult) error
	// Release descarta uma chave não concluída, permitindo reenvio.
	Release(ctx context.Context, key string) error
}

// CanonicalHash devolve o SHA-256 (hex) dos campos de negócio em JSON canônico: chaves ordenadas
// (encoding/json ordena as chaves de mapas), sem a chave de idempotência nem metadados de transporte.
func CanonicalHash(in SubmitInput) string {
	fields := map[string]any{
		"providerId":            in.ProviderID,
		"externalTransactionId": in.ExternalTransactionID,
		"playerId":              in.PlayerID,
		"walletId":              in.WalletID,
		"roundId":               in.RoundID,
		"gameId":                in.GameID,
		"kind":                  in.Kind,
		"money":                 map[string]string{"amount": in.Money.Amount, "currency": in.Money.Currency},
	}
	if in.ReferenceExternalTransactionID != "" {
		fields["referenceExternalTransactionId"] = in.ReferenceExternalTransactionID
	}
	b, _ := json.Marshal(fields) // mapa de strings: não falha
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// IdempotentTransactions garante execução única de Submit por chave de idempotência.
type IdempotentTransactions struct {
	store IdempotencyStore
	next  Transactions
}

var _ Transactions = (*IdempotentTransactions)(nil)

func NewIdempotentTransactions(store IdempotencyStore, next Transactions) *IdempotentTransactions {
	return &IdempotentTransactions{store: store, next: next}
}

// persistTimeout limita Complete e Release, que não devem depender do cancelamento da requisição:
// a operação já pode ter produzido efeito.
const persistTimeout = 5 * time.Second

func (t *IdempotentTransactions) Submit(ctx context.Context, in SubmitInput) (TransactionResult, error) {
	replay, err := t.store.Begin(ctx, in.IdempotencyKey, in.ProviderID, in.ExternalTransactionID, CanonicalHash(in))
	if err != nil {
		return TransactionResult{}, err
	}
	if replay != nil {
		// Resultados ainda pendentes são reavaliados contra o estado atual da transação (que pode ter sido
		// concluída pelo worker desde a primeira resposta); o serviço devolve a transação já gravada, com
		// IdempotentReplay verdadeiro, sem reaplicar a operação.
		if replay.Status == StatusPending || replay.Status == StatusPendingReference {
			return t.next.Submit(ctx, in)
		}
		res := *replay
		res.IdempotentReplay = true
		return res, nil
	}

	res, err := t.next.Submit(ctx, in)
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()
	if err != nil {
		// Falha não é resultado: libera a chave para o cliente reenviar.
		_ = t.store.Release(pctx, in.IdempotencyKey)
		return TransactionResult{}, err
	}
	if err := t.store.Complete(pctx, in.IdempotencyKey, res); err != nil {
		return TransactionResult{}, err
	}
	return res, nil
}

func (t *IdempotentTransactions) GetByID(ctx context.Context, id string) (Transaction, error) {
	return t.next.GetByID(ctx, id)
}

func (t *IdempotentTransactions) GetByExternal(ctx context.Context, providerID, externalID string) (Transaction, error) {
	return t.next.GetByExternal(ctx, providerID, externalID)
}
