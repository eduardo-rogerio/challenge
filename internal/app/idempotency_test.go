package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func baseInput() SubmitInput {
	return SubmitInput{
		ProviderID: "provider-a", ExternalTransactionID: "t-1",
		PlayerID: "p", WalletID: "w", RoundID: "r", GameID: "g", Kind: "BET",
		Money:          Money{Amount: "25.00", Currency: "BRL"},
		IdempotencyKey: "provider-a:t-1",
	}
}

func TestCanonicalHash(t *testing.T) {
	base := baseInput()
	h := CanonicalHash(base)
	if len(h) != 64 {
		t.Fatalf("hash %q não é SHA-256 hex", h)
	}
	if CanonicalHash(base) != h {
		t.Fatal("hash não é determinístico")
	}

	same := base
	same.IdempotencyKey = "outra-chave"
	if CanonicalHash(same) != h {
		t.Error("a chave de idempotência não deve entrar no hash")
	}

	mutations := map[string]func(*SubmitInput){
		"providerId": func(i *SubmitInput) { i.ProviderID = "x" },
		"externalId": func(i *SubmitInput) { i.ExternalTransactionID = "x" },
		"playerId":   func(i *SubmitInput) { i.PlayerID = "x" },
		"walletId":   func(i *SubmitInput) { i.WalletID = "x" },
		"roundId":    func(i *SubmitInput) { i.RoundID = "x" },
		"gameId":     func(i *SubmitInput) { i.GameID = "x" },
		"kind":       func(i *SubmitInput) { i.Kind = "WIN" },
		"amount":     func(i *SubmitInput) { i.Money.Amount = "26.00" },
		"currency":   func(i *SubmitInput) { i.Money.Currency = "USD" },
		"reference":  func(i *SubmitInput) { i.ReferenceExternalTransactionID = "x" },
	}
	for name, mutate := range mutations {
		in := base
		mutate(&in)
		if CanonicalHash(in) == h {
			t.Errorf("alterar %s não mudou o hash", name)
		}
	}
}

// memStore é um IdempotencyStore em memória, só para testes do decorador.
type memStore struct {
	mu          sync.Mutex
	rows        map[string]*memRow
	pairs       map[string]string
	completeErr error
	released    int
}

type memRow struct {
	hash   string
	done   bool
	result TransactionResult
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]*memRow{}, pairs: map[string]string{}}
}

func (m *memStore) Begin(_ context.Context, key, provider, ext, hash string) (*TransactionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pair := provider + "/" + ext
	row, ok := m.rows[key]
	if !ok {
		if other, used := m.pairs[pair]; used && other != key {
			return nil, ErrIdempotencyConflict
		}
		m.rows[key] = &memRow{hash: hash}
		m.pairs[pair] = key
		return nil, nil
	}
	switch {
	case row.hash != hash:
		return nil, ErrIdempotencyConflict
	case row.done:
		r := row.result
		return &r, nil
	default:
		return nil, ErrInProgress
	}
}

func (m *memStore) Complete(_ context.Context, key string, res TransactionResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.completeErr != nil {
		return m.completeErr
	}
	m.rows[key].done, m.rows[key].result = true, res
	return nil
}

func (m *memStore) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released++
	delete(m.rows, key)
	for p, k := range m.pairs {
		if k == key {
			delete(m.pairs, p)
		}
	}
	return nil
}

// stubQueries implementa as consultas de Transactions devolvendo ErrNotFound.
type stubQueries struct{}

func (stubQueries) GetByID(context.Context, string) (Transaction, error) {
	return Transaction{}, ErrNotFound
}

func (stubQueries) GetByExternal(context.Context, string, string) (Transaction, error) {
	return Transaction{}, ErrNotFound
}

// countingTx conta chamadas a Submit e devolve results/erros programados.
type countingTx struct {
	stubQueries
	calls  atomic.Int32
	result TransactionResult
	errs   []error // consumidos na ordem; vazio devolve sucesso
	gate   chan struct{}
}

func (c *countingTx) Submit(context.Context, SubmitInput) (TransactionResult, error) {
	n := int(c.calls.Add(1))
	if c.gate != nil {
		<-c.gate
	}
	if n <= len(c.errs) && c.errs[n-1] != nil {
		return TransactionResult{}, c.errs[n-1]
	}
	return c.result, nil
}

func processed() TransactionResult {
	bal := Money{Amount: "975.00", Currency: "BRL"}
	return TransactionResult{TransactionID: "tx-1", Status: StatusProcessed, Balance: &bal}
}

func TestSubmitExecutesOnceAndReplays(t *testing.T) {
	next := &countingTx{result: processed()}
	svc := NewIdempotentTransactions(newMemStore(), next)

	first, err := svc.Submit(context.Background(), baseInput())
	if err != nil || first.IdempotentReplay {
		t.Fatalf("primeira chamada: %+v, %v", first, err)
	}
	second, err := svc.Submit(context.Background(), baseInput())
	if err != nil {
		t.Fatal(err)
	}
	if !second.IdempotentReplay || second.TransactionID != "tx-1" || second.Balance == nil || second.Balance.Amount != "975.00" {
		t.Fatalf("replay inesperado: %+v", second)
	}
	if next.calls.Load() != 1 {
		t.Fatalf("next chamado %d vezes", next.calls.Load())
	}
}

func TestSubmitReplaysRejectedWithFailureCode(t *testing.T) {
	next := &countingTx{result: TransactionResult{TransactionID: "tx-2", Status: StatusRejected, FailureCode: "INSUFFICIENT_FUNDS"}}
	svc := NewIdempotentTransactions(newMemStore(), next)
	_, _ = svc.Submit(context.Background(), baseInput())
	res, err := svc.Submit(context.Background(), baseInput())
	if err != nil || res.Status != StatusRejected || res.FailureCode != "INSUFFICIENT_FUNDS" || !res.IdempotentReplay {
		t.Fatalf("replay: %+v, %v", res, err)
	}
}

func TestSubmitConflicts(t *testing.T) {
	next := &countingTx{result: processed()}
	svc := NewIdempotentTransactions(newMemStore(), next)
	if _, err := svc.Submit(context.Background(), baseInput()); err != nil {
		t.Fatal(err)
	}

	diff := baseInput()
	diff.Money.Amount = "99.00"
	if _, err := svc.Submit(context.Background(), diff); !errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("mesma chave, conteúdo diferente: %v", err)
	}

	otherKey := baseInput()
	otherKey.IdempotencyKey = "outra"
	if _, err := svc.Submit(context.Background(), otherKey); !errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("mesma operação externa, outra chave: %v", err)
	}
	if next.calls.Load() != 1 {
		t.Fatalf("next chamado %d vezes", next.calls.Load())
	}
}

func TestSubmitInProgress(t *testing.T) {
	next := &countingTx{result: processed(), gate: make(chan struct{})}
	svc := NewIdempotentTransactions(newMemStore(), next)

	done := make(chan error, 1)
	go func() { _, err := svc.Submit(context.Background(), baseInput()); done <- err }()
	for next.calls.Load() == 0 {
	}
	if _, err := svc.Submit(context.Background(), baseInput()); !errors.Is(err, ErrInProgress) {
		t.Fatalf("esperava ErrInProgress, obtido %v", err)
	}
	close(next.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSubmitErrorReleasesKey(t *testing.T) {
	store := newMemStore()
	next := &countingTx{result: processed(), errs: []error{ErrUnavailable}}
	svc := NewIdempotentTransactions(store, next)

	if _, err := svc.Submit(context.Background(), baseInput()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("primeira chamada: %v", err)
	}
	res, err := svc.Submit(context.Background(), baseInput())
	if err != nil || res.IdempotentReplay {
		t.Fatalf("segunda chamada deveria executar de novo: %+v, %v", res, err)
	}
	if next.calls.Load() != 2 || store.released != 1 {
		t.Fatalf("calls=%d released=%d", next.calls.Load(), store.released)
	}
}

func TestSubmitCompleteFailureReturnsError(t *testing.T) {
	store := newMemStore()
	store.completeErr = errors.New("db caiu")
	svc := NewIdempotentTransactions(store, &countingTx{result: processed()})
	if _, err := svc.Submit(context.Background(), baseInput()); err == nil {
		t.Fatal("esperava erro quando Complete falha")
	}
}

func TestSubmitConcurrentSameKeyExecutesOnce(t *testing.T) {
	next := &countingTx{result: processed()}
	svc := NewIdempotentTransactions(newMemStore(), next)

	var wg sync.WaitGroup
	var replays, inProgress, firsts atomic.Int32
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.Submit(context.Background(), baseInput())
			switch {
			case errors.Is(err, ErrInProgress):
				inProgress.Add(1)
			case err != nil:
				t.Errorf("erro inesperado: %v", err)
			case res.IdempotentReplay:
				replays.Add(1)
			default:
				firsts.Add(1)
			}
		}()
	}
	wg.Wait()
	if next.calls.Load() != 1 || firsts.Load() != 1 {
		t.Fatalf("next=%d firsts=%d replays=%d inProgress=%d", next.calls.Load(), firsts.Load(), replays.Load(), inProgress.Load())
	}
}

func TestQueriesAreDelegated(t *testing.T) {
	svc := NewIdempotentTransactions(newMemStore(), &countingTx{})
	if _, err := svc.GetByID(context.Background(), "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: %v", err)
	}
	if _, err := svc.GetByExternal(context.Background(), "p", "e"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByExternal: %v", err)
	}
}

// Um replay de resultado ainda pendente é reavaliado pelo serviço, que conhece o estado atual.
func TestSubmitReplayOfPendingResultRefreshesFromService(t *testing.T) {
	pending := TransactionResult{TransactionID: "tx-p", Status: StatusPendingReference}
	next := &countingTx{result: pending}
	svc := NewIdempotentTransactions(newMemStore(), next)
	if _, err := svc.Submit(context.Background(), baseInput()); err != nil {
		t.Fatal(err)
	}

	// o worker concluiu a pendência: o serviço agora devolve o estado atual, marcado como replay
	bal := Money{Amount: "100.00", Currency: "BRL"}
	next.result = TransactionResult{TransactionID: "tx-p", Status: StatusProcessed, Balance: &bal, IdempotentReplay: true}
	res, err := svc.Submit(context.Background(), baseInput())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusProcessed || res.Balance == nil || res.Balance.Amount != "100.00" || !res.IdempotentReplay || res.TransactionID != "tx-p" {
		t.Fatalf("replay deveria refletir o estado atual: %+v", res)
	}
	if next.calls.Load() != 2 {
		t.Fatalf("o serviço deveria ser consultado no replay de pendência; chamadas = %d", next.calls.Load())
	}
}

func TestSubmitReplayOfPendingResultStillPending(t *testing.T) {
	pending := TransactionResult{TransactionID: "tx-p", Status: StatusPending}
	next := &countingTx{result: pending}
	svc := NewIdempotentTransactions(newMemStore(), next)
	_, _ = svc.Submit(context.Background(), baseInput())
	next.result.IdempotentReplay = true
	res, err := svc.Submit(context.Background(), baseInput())
	if err != nil || res.Status != StatusPending || !res.IdempotentReplay || res.TransactionID != "tx-p" {
		t.Fatalf("replay de pendência aberta: %+v, %v", res, err)
	}
}

func TestSubmitReplayOfPendingPropagatesServiceError(t *testing.T) {
	next := &countingTx{result: TransactionResult{TransactionID: "tx-p", Status: StatusPendingReference}}
	svc := NewIdempotentTransactions(newMemStore(), next)
	_, _ = svc.Submit(context.Background(), baseInput())
	next.errs = []error{nil, ErrUnavailable} // segunda chamada falha
	if _, err := svc.Submit(context.Background(), baseInput()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("erro do serviço deveria ser propagado: %v", err)
	}
}

// Resultados terminais continuam sendo devolvidos como persistidos, sem consultar o serviço.
func TestSubmitReplayOfTerminalResultDoesNotCallService(t *testing.T) {
	next := &countingTx{result: processed()}
	svc := NewIdempotentTransactions(newMemStore(), next)
	_, _ = svc.Submit(context.Background(), baseInput())
	_, _ = svc.Submit(context.Background(), baseInput())
	if next.calls.Load() != 1 {
		t.Fatalf("chamadas = %d", next.calls.Load())
	}
}
