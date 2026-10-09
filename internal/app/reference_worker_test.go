package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type scriptedResolver struct {
	mu      sync.Mutex
	results []struct {
		n   int
		err error
	}
	calls int
	times []time.Time
}

func (s *scriptedResolver) ResolveBatch(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.times = append(s.times, time.Now())
	if len(s.results) == 0 {
		return 0, nil
	}
	r := s.results[0]
	s.results = s.results[1:]
	return r.n, r.err
}

func (s *scriptedResolver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newTestWorker(r batchResolver, interval time.Duration, batch int) *PendingReferenceWorker {
	return &PendingReferenceWorker{resolver: r, interval: interval, batch: batch, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func runWorker(w *PendingReferenceWorker) (cancel context.CancelFunc, done <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	go func() { defer close(ch); w.Run(ctx) }()
	return cancel, ch
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado esperando: %s", what)
}

func TestWorkerScansRepeatedlyAndStopsOnCancel(t *testing.T) {
	r := &scriptedResolver{}
	cancel, done := runWorker(newTestWorker(r, 10*time.Millisecond, 5))
	waitFor(t, "várias varreduras", func() bool { return r.callCount() >= 3 })

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("o laço não terminou depois do cancelamento")
	}
	n := r.callCount()
	time.Sleep(50 * time.Millisecond)
	if r.callCount() != n {
		t.Error("o laço continuou varrendo depois de terminar")
	}
}

func TestWorkerContinuesImmediatelyWhileBatchIsFull(t *testing.T) {
	r := &scriptedResolver{results: []struct {
		n   int
		err error
	}{{5, nil}, {5, nil}, {5, nil}, {2, nil}}}
	// intervalo longo: só as varreduras com lote cheio acontecem em seguida
	cancel, done := runWorker(newTestWorker(r, time.Hour, 5))
	defer func() { cancel(); <-done }()

	waitFor(t, "quatro varreduras seguidas", func() bool { return r.callCount() >= 4 })
	time.Sleep(50 * time.Millisecond)
	if r.callCount() != 4 {
		t.Fatalf("depois do lote incompleto o laço deveria esperar o intervalo; varreduras = %d", r.callCount())
	}
}

func TestWorkerSurvivesErrors(t *testing.T) {
	r := &scriptedResolver{results: []struct {
		n   int
		err error
	}{{0, ErrUnavailable}, {0, errors.New("outro erro")}, {1, nil}}}
	cancel, done := runWorker(newTestWorker(r, 5*time.Millisecond, 5))
	defer func() { cancel(); <-done }()
	waitFor(t, "varreduras depois dos erros", func() bool { return r.callCount() >= 4 })
}

func TestWorkerStopsEvenWhenCancelledBeforeStart(t *testing.T) {
	r := &scriptedResolver{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { defer close(done); newTestWorker(r, time.Hour, 5).Run(ctx) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("o laço deveria terminar de imediato com o contexto cancelado")
	}
}
