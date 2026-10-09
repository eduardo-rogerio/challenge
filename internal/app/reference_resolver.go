package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"desafio/internal/domain"
)

// Desfechos de uma tentativa do worker, usados em logs e métricas.
const (
	OutcomeProcessed = "processed"
	OutcomeRejected  = "rejected"
	OutcomeRetry     = "retry"
	OutcomeExpired   = "expired"
)

// PendingReferenceResolver retoma as reversões em PENDING_REFERENCE. Não tem regra financeira própria:
// reavalia cada pendência com as mesmas regras do envio (domain.Apply), sob o lock da carteira.
type PendingReferenceResolver struct {
	store   Store
	ids     IDGenerator
	clock   Clock
	policy  ReferencePolicy
	log     *slog.Logger
	observe func(outcome string)
	jitter  func() float64
}

type ResolverOption func(*PendingReferenceResolver)

func WithResolverLogger(l *slog.Logger) ResolverOption {
	return func(r *PendingReferenceResolver) { r.log = l }
}

// WithOutcomeObserver registra uma função chamada a cada desfecho (por exemplo, para uma métrica).
func WithOutcomeObserver(f func(outcome string)) ResolverOption {
	return func(r *PendingReferenceResolver) { r.observe = f }
}

// WithJitter troca a fonte do sorteio em [0,1) usada no backoff (para testes determinísticos).
func WithJitter(f func() float64) ResolverOption {
	return func(r *PendingReferenceResolver) { r.jitter = f }
}

func NewPendingReferenceResolver(store Store, ids IDGenerator, clock Clock, policy ReferencePolicy, opts ...ResolverOption) *PendingReferenceResolver {
	r := &PendingReferenceResolver{store: store, ids: ids, clock: clock, policy: policy, log: slog.Default(), jitter: rand.Float64}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ResolveBatch reivindica um lote de pendências vencidas e processa cada uma em sua própria transação.
// Devolve quantas foram reivindicadas. Uma falha ao processar um item é registrada e não interrompe os demais:
// a lease expira e o item é reivindicado de novo.
func (r *PendingReferenceResolver) ResolveBatch(ctx context.Context) (int, error) {
	now := r.clock.Now()
	var items []PendingReference
	err := r.store.InTx(ctx, func(ctx context.Context, repos Repos) error {
		var err error
		items, err = repos.PendingReferences().Claim(ctx, now, now.Add(r.policy.ClaimLease), r.policy.BatchSize)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("reivindicar pendências: %w", err)
	}
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		if err := r.ResolveItem(ctx, it); err != nil {
			r.log.ErrorContext(ctx, "falha ao resolver pendência", "transactionId", it.ID.String(), "walletId", it.WalletID.String(), "error", err)
		}
	}
	return len(items), nil
}

// ResolveItem reavalia uma pendência reivindicada, em uma transação SQL: trava a carteira (mesma ordem de
// locks do envio), confirma que a transação ainda está pendente e aplica as regras do domínio.
func (r *PendingReferenceResolver) ResolveItem(ctx context.Context, it PendingReference) error {
	var outcome string
	err := r.store.InTx(ctx, func(ctx context.Context, repos Repos) error {
		w, err := repos.Wallets().GetForUpdate(ctx, it.WalletID)
		if err != nil {
			return err
		}
		tx, err := repos.Transactions().GetByID(ctx, it.ID)
		if err != nil {
			return err
		}
		if tx.Status() != domain.StatusPendingReference {
			return nil // resolvida por outro caminho entre a reivindicação e o lock
		}

		now := r.clock.Now()
		for _, floor := range []time.Time{w.UpdatedAt(), tx.UpdatedAt()} {
			if now.Before(floor) {
				now = floor
			}
		}
		ref, err := resolveReference(ctx, repos, tx)
		if err != nil {
			return err
		}
		expectedVersion := w.Version()
		entry, err := domain.Apply(w, tx, ref, r.ids.NewID(), now)
		if err != nil {
			return err
		}

		switch tx.Status() {
		case domain.StatusProcessed, domain.StatusRejected:
			if entry != nil {
				if err := repos.Wallets().Update(ctx, w, expectedVersion); err != nil {
					return err
				}
				if err := repos.Ledger().Insert(ctx, entry); err != nil {
					return err
				}
			}
			if err := repos.Transactions().UpdateOutcome(ctx, tx); err != nil {
				return err
			}
			outcome = OutcomeProcessed
			if tx.Status() == domain.StatusRejected {
				outcome = OutcomeRejected
			}
		default: // continua PENDING_REFERENCE: nova tentativa ou expiração
			attempts := it.Attempts + 1
			if r.policy.Exhausted(attempts, tx.CreatedAt(), now) {
				code := domain.FailureReferenceNotFound
				if ref.Tx != nil {
					code = domain.FailureReferenceNotProcessed // a referência existe, mas nunca concluiu com sucesso
				}
				if err := tx.Reject(code, now); err != nil {
					return err
				}
				if err := repos.Transactions().UpdateOutcome(ctx, tx); err != nil {
					return err
				}
				outcome = OutcomeExpired
				break
			}
			next := now.Add(r.policy.Backoff(attempts, JitterFactor(r.jitter())))
			if err := repos.PendingReferences().Reschedule(ctx, it.ID, attempts, next); err != nil {
				return err
			}
			outcome = OutcomeRetry
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // carteira ou transação inexistente: nada a fazer
		}
		return err
	}
	if outcome != "" {
		r.log.InfoContext(ctx, "pendência de referência", "outcome", outcome,
			"transactionId", it.ID.String(), "walletId", it.WalletID.String())
		if r.observe != nil {
			r.observe(outcome)
		}
	}
	return nil
}
