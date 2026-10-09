package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"desafio/internal/domain"
)

const (
	defaultLedgerLimit = 50
	maxLedgerLimit     = 100
	cursorPrefix       = "v1:"
)

// WalletService implementa Wallets sobre o armazenamento persistente.
type WalletService struct {
	store        Store
	ids          IDGenerator
	clock        Clock
	log          *slog.Logger
	onDivergence func(walletID string)
}

var _ Wallets = (*WalletService)(nil)

type WalletOption func(*WalletService)

// WithLogger define o logger usado para registrar divergências de reconciliação.
func WithLogger(l *slog.Logger) WalletOption { return func(s *WalletService) { s.log = l } }

// WithDivergenceHook registra uma função chamada a cada divergência encontrada (por exemplo, para uma métrica).
func WithDivergenceHook(f func(walletID string)) WalletOption {
	return func(s *WalletService) { s.onDivergence = f }
}

func NewWalletService(store Store, ids IDGenerator, clock Clock, opts ...WalletOption) *WalletService {
	s := &WalletService{store: store, ids: ids, clock: clock, log: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Open grava, no mesmo commit, a carteira e, com saldo inicial positivo, a transação OPENING e o lançamento.
func (s *WalletService) Open(ctx context.Context, in OpenWalletInput) (Wallet, error) {
	playerID, err := toDomainID(in.PlayerID)
	if err != nil {
		return Wallet{}, err
	}
	initial, err := toDomainMoney(in.InitialBalance)
	if err != nil {
		return Wallet{}, err
	}
	opening, err := domain.OpenWallet(s.ids.NewID(), playerID, s.ids.NewID(), s.ids.NewID(), initial, s.clock.Now())
	if err != nil {
		return Wallet{}, invalidInput(err)
	}

	err = s.store.InTx(ctx, func(ctx context.Context, r Repos) error {
		if err := r.Wallets().Insert(ctx, opening.Wallet); err != nil {
			return err
		}
		if opening.Transaction == nil {
			return nil
		}
		if err := r.Transactions().Insert(ctx, opening.Transaction); err != nil {
			return err
		}
		return r.Ledger().Insert(ctx, opening.Entry)
	})
	if err != nil {
		return Wallet{}, err
	}
	return walletToDTO(opening.Wallet), nil
}

func (s *WalletService) Get(ctx context.Context, walletID string) (Wallet, error) {
	id, err := toDomainID(walletID)
	if err != nil {
		return Wallet{}, err
	}
	var out Wallet
	err = s.store.Read(ctx, func(ctx context.Context, r Repos) error {
		w, err := r.Wallets().Get(ctx, id)
		if err != nil {
			return err
		}
		out = walletToDTO(w)
		return nil
	})
	return out, err
}

// Ledger lista os lançamentos em ordem estável de criação, com cursor opaco.
func (s *WalletService) Ledger(ctx context.Context, walletID, cursor string, limit int) (LedgerPage, error) {
	id, err := toDomainID(walletID)
	if err != nil {
		return LedgerPage{}, err
	}
	if limit == 0 {
		limit = defaultLedgerLimit
	}
	if limit < 1 || limit > maxLedgerLimit {
		return LedgerPage{}, fmt.Errorf("%w: limit deve estar entre 1 e %d", ErrInvalidInput, maxLedgerLimit)
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}

	page := LedgerPage{Entries: []LedgerEntry{}}
	err = s.store.Read(ctx, func(ctx context.Context, r Repos) error {
		if _, err := r.Wallets().Get(ctx, id); err != nil {
			return err
		}
		rows, err := r.Ledger().List(ctx, id, after, limit+1)
		if err != nil {
			return err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		for _, row := range rows {
			page.Entries = append(page.Entries, ledgerEntryToDTO(row.Entry))
		}
		if more {
			next := encodeCursor(rows[len(rows)-1].Seq)
			page.NextCursor = &next
		}
		return nil
	})
	return page, err
}

// Reconcile reconstrói o saldo a partir do ledger e o compara ao saldo armazenado, em uma visão
// consistente e somente leitura. Divergências são registradas, mas nunca corrigidas.
func (s *WalletService) Reconcile(ctx context.Context, walletID string) (Reconciliation, error) {
	id, err := toDomainID(walletID)
	if err != nil {
		return Reconciliation{}, err
	}
	var out Reconciliation
	err = s.store.Read(ctx, func(ctx context.Context, r Repos) error {
		w, err := r.Wallets().Get(ctx, id)
		if err != nil {
			return err
		}
		totals, err := r.Ledger().Totals(ctx, id, w.Currency())
		if err != nil {
			return err
		}
		calculated, err := totals.Credits.Sub(totals.Debits)
		if err != nil {
			return err
		}
		diff, err := w.Balance().Sub(calculated)
		if err != nil {
			return err
		}
		out = Reconciliation{
			WalletID:          walletID,
			StoredBalance:     fromDomainMoney(w.Balance()),
			CalculatedBalance: fromDomainMoney(calculated),
			Difference:        fromDomainMoney(diff),
			Consistent:        diff.IsZero(),
			CheckedEntries:    totals.Count,
		}
		return nil
	})
	if err != nil {
		return Reconciliation{}, err
	}
	if !out.Consistent {
		s.log.ErrorContext(ctx, "divergência de reconciliação", "walletId", walletID,
			"stored", out.StoredBalance.Amount, "calculated", out.CalculatedBalance.Amount, "difference", out.Difference.Amount)
		if s.onDivergence != nil {
			s.onDivergence(walletID)
		}
	}
	return out, nil
}

func encodeCursor(seq int64) string {
	return encodeRaw([]byte(cursorPrefix + strconv.FormatInt(seq, 10)))
}

func encodeRaw(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), cursorPrefix) {
		return 0, fmt.Errorf("%w: cursor inválido", ErrInvalidInput)
	}
	seq, err := strconv.ParseInt(strings.TrimPrefix(string(raw), cursorPrefix), 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("%w: cursor inválido", ErrInvalidInput)
	}
	return seq, nil
}
