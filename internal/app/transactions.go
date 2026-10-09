package app

import (
	"context"
	"errors"

	"desafio/internal/domain"
)

// TransactionService implementa Transactions: aplica as regras do domínio sob o lock da carteira
// e grava saldo, lançamento e transação no mesmo commit.
type TransactionService struct {
	store Store
	ids   IDGenerator
	clock Clock
}

var _ Transactions = (*TransactionService)(nil)

func NewTransactionService(store Store, ids IDGenerator, clock Clock) *TransactionService {
	return &TransactionService{store: store, ids: ids, clock: clock}
}

// Submit processa uma operação de forma síncrona. Rejeições de negócio são gravadas (REJECTED) e
// devolvidas como resultado; erros indicam entrada inválida, recurso inexistente, conflito de
// idempotência ou indisponibilidade, e nesses casos nada é gravado.
func (s *TransactionService) Submit(ctx context.Context, in SubmitInput) (TransactionResult, error) {
	walletID, err := toDomainID(in.WalletID)
	if err != nil {
		return TransactionResult{}, err
	}
	playerID, err := toDomainID(in.PlayerID)
	if err != nil {
		return TransactionResult{}, err
	}
	money, err := toDomainMoney(in.Money)
	if err != nil {
		return TransactionResult{}, err
	}
	hash := CanonicalHash(in)

	var result TransactionResult
	err = s.store.InTx(ctx, func(ctx context.Context, r Repos) error {
		// O lock da carteira serializa as operações dela; carteiras diferentes não se bloqueiam.
		w, err := r.Wallets().GetForUpdate(ctx, walletID)
		if err != nil {
			return err
		}

		// A mesma operação externa já gravada: replay se a chave e o conteúdo forem os mesmos.
		existing, err := r.Transactions().GetByExternal(ctx, in.ProviderID, in.ExternalTransactionID)
		switch {
		case err == nil:
			if existing.IdempotencyKey() != in.IdempotencyKey || existing.RequestHash() != hash {
				return ErrIdempotencyConflict
			}
			result = resultOf(existing, true)
			return nil
		case !errors.Is(err, ErrNotFound):
			return err
		}

		// Entre instâncias com relógios diferentes, o horário nunca pode retroceder em relação à carteira.
		now := s.clock.Now()
		if now.Before(w.UpdatedAt()) {
			now = w.UpdatedAt()
		}
		tx, err := domain.NewExternalTransaction(domain.ExternalParams{
			ID: s.ids.NewID(), ProviderID: in.ProviderID, ExternalID: in.ExternalTransactionID,
			IdempotencyKey: in.IdempotencyKey, RequestHash: hash, WalletID: walletID, PlayerID: playerID,
			RoundID: in.RoundID, GameID: in.GameID, Kind: domain.Kind(in.Kind), Money: money,
			ReferenceExternalID: in.ReferenceExternalTransactionID, Now: now,
		})
		if err != nil {
			return invalidInput(err)
		}
		ref, err := resolveReference(ctx, r, tx)
		if err != nil {
			return err
		}

		expectedVersion := w.Version()
		entry, err := domain.Apply(w, tx, ref, s.ids.NewID(), now)
		if err != nil {
			return err
		}
		if entry != nil {
			if err := r.Wallets().Update(ctx, w, expectedVersion); err != nil {
				return err
			}
		}
		if err := r.Transactions().Insert(ctx, tx); err != nil {
			return err
		}
		if entry != nil {
			if err := r.Ledger().Insert(ctx, entry); err != nil {
				return err
			}
		}
		result = resultOf(tx, false)
		return nil
	})
	if err != nil {
		return TransactionResult{}, err
	}
	return result, nil
}

// resolveReference (compartilhada com o worker de referências pendentes) procura a transação referenciada por (provedor, id externo) e, nas reversões,
// informa se ela já recebeu uma reversão bem-sucedida. Referência inexistente não é erro: o domínio
// deixa a reversão em PENDING_REFERENCE.
func resolveReference(ctx context.Context, r Repos, tx *domain.WagerTransaction) (domain.Reference, error) {
	if tx.ReferenceExternalID() == "" {
		return domain.Reference{}, nil
	}
	ref, err := r.Transactions().GetByExternal(ctx, tx.ProviderID(), tx.ReferenceExternalID())
	if errors.Is(err, ErrNotFound) {
		return domain.Reference{}, nil
	}
	if err != nil {
		return domain.Reference{}, err
	}
	reversed := false
	if (tx.Kind() == domain.KindRefund || tx.Kind() == domain.KindRollback) && ref.Status() == domain.StatusProcessed {
		if reversed, err = r.Transactions().HasProcessedReversal(ctx, ref.ID()); err != nil {
			return domain.Reference{}, err
		}
	}
	return domain.Reference{Tx: ref, Reversed: reversed}, nil
}

func (s *TransactionService) GetByID(ctx context.Context, transactionID string) (Transaction, error) {
	id, err := toDomainID(transactionID)
	if err != nil {
		return Transaction{}, err
	}
	var out Transaction
	err = s.store.Read(ctx, func(ctx context.Context, r Repos) error {
		tx, err := r.Transactions().GetByID(ctx, id)
		if err != nil {
			return err
		}
		out = transactionToDTO(tx)
		return nil
	})
	return out, err
}

func (s *TransactionService) GetByExternal(ctx context.Context, providerID, externalTransactionID string) (Transaction, error) {
	var out Transaction
	err := s.store.Read(ctx, func(ctx context.Context, r Repos) error {
		tx, err := r.Transactions().GetByExternal(ctx, providerID, externalTransactionID)
		if err != nil {
			return err
		}
		out = transactionToDTO(tx)
		return nil
	})
	return out, err
}
