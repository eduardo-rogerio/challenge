package domain

import (
	"errors"
	"testing"
	"time"
)

func newWallet(t *testing.T, balance string) *Wallet {
	t.Helper()
	w, err := NewWallet(idWallet, idPlayer, mustMoney(t, balance), t0)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestNewWallet(t *testing.T) {
	w := newWallet(t, "1000.00")
	if w.Version() != 1 || w.Balance().Amount() != "1000.00" || w.Currency() != "BRL" ||
		!w.CreatedAt().Equal(t0) || !w.UpdatedAt().Equal(t0) || w.ID() != idWallet || w.PlayerID() != idPlayer {
		t.Fatalf("carteira inesperada: %+v", w)
	}
}

func TestNewWalletInvalid(t *testing.T) {
	ok := mustMoney(t, "1.00")
	tests := map[string]func() error{
		"id vazio":       func() error { _, err := NewWallet("", idPlayer, ok, t0); return err },
		"id malformado":  func() error { _, err := NewWallet("abc", idPlayer, ok, t0); return err },
		"jogador vazio":  func() error { _, err := NewWallet(idWallet, "", ok, t0); return err },
		"saldo não init": func() error { _, err := NewWallet(idWallet, idPlayer, Money{}, t0); return err },
		"saldo negativo": func() error { _, err := NewWallet(idWallet, idPlayer, mustNeg(t, ok), t0); return err },
		"sem horário":    func() error { _, err := NewWallet(idWallet, idPlayer, ok, time0()); return err },
	}
	for name, fn := range tests {
		if err := fn(); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func TestRehydrateWalletKeepsState(t *testing.T) {
	later := t0.Add(time.Hour)
	w, err := RehydrateWallet(idWallet, idPlayer, mustMoney(t, "20.00"), 7, t0, later)
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance().Amount() != "20.00" || w.Version() != 7 || !w.CreatedAt().Equal(t0) || !w.UpdatedAt().Equal(later) {
		t.Fatalf("estado alterado na reidratação: %+v", w)
	}
}

func TestRehydrateWalletInvalid(t *testing.T) {
	bal := mustMoney(t, "1.00")
	if _, err := RehydrateWallet(idWallet, idPlayer, mustNeg(t, bal), 1, t0, t0); !errors.Is(err, ErrInvalidState) {
		t.Errorf("saldo negativo: %v", err)
	}
	if _, err := RehydrateWallet(idWallet, idPlayer, bal, 0, t0, t0); !errors.Is(err, ErrInvalidState) {
		t.Errorf("versão 0: %v", err)
	}
	if _, err := RehydrateWallet(idWallet, idPlayer, bal, 1, t0, t0.Add(-time.Second)); !errors.Is(err, ErrInvalidState) {
		t.Errorf("updatedAt antes de createdAt: %v", err)
	}
	if _, err := RehydrateWallet(idWallet, idPlayer, Money{}, 1, t0, t0); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("saldo não inicializado: %v", err)
	}
}

func TestWalletCredit(t *testing.T) {
	w := newWallet(t, "100.00")
	later := t0.Add(time.Minute)
	e, err := w.Credit(idEntry1, idTx1, mustMoney(t, "25.00"), later)
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance().Amount() != "125.00" || w.Version() != 2 || !w.UpdatedAt().Equal(later) {
		t.Fatalf("carteira: %s v%d", w.Balance(), w.Version())
	}
	if e.Direction() != DirectionCredit || e.BalanceBefore().Amount() != "100.00" || e.BalanceAfter().Amount() != "125.00" ||
		e.WalletID() != idWallet || e.TransactionID() != idTx1 {
		t.Fatalf("lançamento: %+v", e)
	}
}

func TestWalletDebit(t *testing.T) {
	w := newWallet(t, "100.00")
	e, err := w.Debit(idEntry1, idTx1, mustMoney(t, "25.00"), t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance().Amount() != "75.00" || w.Version() != 2 {
		t.Fatalf("carteira: %s v%d", w.Balance(), w.Version())
	}
	if e.Direction() != DirectionDebit || e.BalanceBefore().Amount() != "100.00" || e.BalanceAfter().Amount() != "75.00" {
		t.Fatalf("lançamento: %+v", e)
	}
	// debitar exatamente o saldo é permitido
	if _, err := w.Debit(idEntry2, idTx2, mustMoney(t, "75.00"), t0.Add(2*time.Minute)); err != nil || !w.Balance().IsZero() {
		t.Fatalf("débito do saldo inteiro: %v", err)
	}
}

func TestWalletFailuresLeaveWalletUntouched(t *testing.T) {
	usd, _ := ParseMoney("1.00", "USD")
	big := mustMoney(t, "92233720368547758.07")
	tests := map[string]struct {
		do   func(w *Wallet) error
		want error
	}{
		"saldo insuficiente": {func(w *Wallet) error {
			_, err := w.Debit(idEntry1, idTx1, mustMoney(t, "100.01"), t0.Add(time.Hour))
			return err
		}, ErrInsufficientFunds},
		"moeda divergente": {func(w *Wallet) error { _, err := w.Credit(idEntry1, idTx1, usd, t0.Add(time.Hour)); return err }, ErrCurrencyMismatch},
		"valor zero": {func(w *Wallet) error {
			_, err := w.Credit(idEntry1, idTx1, mustMoney(t, "0.00"), t0.Add(time.Hour))
			return err
		}, ErrInvalidMoney},
		"valor negativo": {func(w *Wallet) error {
			_, err := w.Debit(idEntry1, idTx1, mustNeg(t, mustMoney(t, "1.00")), t0.Add(time.Hour))
			return err
		}, ErrInvalidMoney},
		"valor não init": {func(w *Wallet) error { _, err := w.Credit(idEntry1, idTx1, Money{}, t0.Add(time.Hour)); return err }, ErrInvalidMoney},
		"overflow":       {func(w *Wallet) error { _, err := w.Credit(idEntry1, idTx1, big, t0.Add(time.Hour)); return err }, ErrOverflow},
		"id de lançamento": {func(w *Wallet) error {
			_, err := w.Credit("", idTx1, mustMoney(t, "1.00"), t0.Add(time.Hour))
			return err
		}, ErrInvalidID},
	}
	for name, tt := range tests {
		w := newWallet(t, "100.00")
		if err := tt.do(w); !errors.Is(err, tt.want) {
			t.Errorf("%s: erro %v, esperado %v", name, err, tt.want)
		}
		if w.Balance().Amount() != "100.00" || w.Version() != 1 || !w.UpdatedAt().Equal(t0) {
			t.Errorf("%s: a carteira mudou: %s v%d", name, w.Balance(), w.Version())
		}
	}
}

func TestOpenWalletWithBalance(t *testing.T) {
	o, err := OpenWallet(idWallet, idPlayer, idTx1, idEntry1, mustMoney(t, "1000.00"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if o.Wallet.Version() != 1 || o.Wallet.Balance().Amount() != "1000.00" {
		t.Fatalf("carteira: %s v%d", o.Wallet.Balance(), o.Wallet.Version())
	}
	tx := o.Transaction
	if tx == nil || tx.Kind() != KindOpening || tx.Status() != StatusProcessed || tx.ProviderID() != "" ||
		tx.WalletID() != idWallet || tx.PlayerID() != idPlayer || tx.ID() != idTx1 {
		t.Fatalf("transação: %+v", tx)
	}
	if b, ok := tx.ResultBalance(); !ok || b.Amount() != "1000.00" {
		t.Fatalf("saldo observado: %v, %v", b, ok)
	}
	e := o.Entry
	if e == nil || e.Direction() != DirectionCredit || e.BalanceBefore().Amount() != "0.00" || e.BalanceAfter().Amount() != "1000.00" ||
		e.TransactionID() != idTx1 || e.ID() != idEntry1 {
		t.Fatalf("lançamento: %+v", e)
	}
}

func TestOpenWalletZeroBalance(t *testing.T) {
	zero, _ := ZeroMoney("BRL")
	o, err := OpenWallet(idWallet, idPlayer, idTx1, idEntry1, zero, t0)
	if err != nil {
		t.Fatal(err)
	}
	if o.Wallet.Version() != 1 || !o.Wallet.Balance().IsZero() || o.Transaction != nil || o.Entry != nil {
		t.Fatalf("saldo zero deve gerar só a carteira: %+v", o)
	}
}

func TestOpenWalletInvalid(t *testing.T) {
	if _, err := OpenWallet(idWallet, idPlayer, idTx1, idEntry1, mustNeg(t, mustMoney(t, "1.00")), t0); err == nil {
		t.Error("saldo negativo deveria falhar")
	}
	if _, err := OpenWallet(idWallet, idPlayer, "x", idEntry1, mustMoney(t, "1.00"), t0); err == nil {
		t.Error("id de transação inválido deveria falhar")
	}
	if _, err := OpenWallet(idWallet, idPlayer, idTx1, idEntry1, Money{}, t0); err == nil {
		t.Error("saldo não inicializado deveria falhar")
	}
}
