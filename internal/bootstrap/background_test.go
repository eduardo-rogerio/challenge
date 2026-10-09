package bootstrap

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// build monta uma aplicação com um "pool" (registrado primeiro) e um worker em segundo plano.
func build(rec *recorder, stopTimeout time.Duration, run func(context.Context)) *fx.App {
	return fx.New(
		fx.NopLogger,
		fx.Supply(testLogger()),
		fx.StopTimeout(stopTimeout),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStop: func(context.Context) error { rec.add("pool fechado"); return nil }})
		}),
		fx.Invoke(func(lc fx.Lifecycle) { runInBackground(lc, "teste", testLogger(), run) }),
	)
}

func TestBackgroundWorkerStopsBeforeDependenciesClose(t *testing.T) {
	rec := &recorder{}
	app := build(rec, 2*time.Second, func(ctx context.Context) {
		rec.add("worker iniciado")
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // conclui o item em andamento
		rec.add("worker terminou")
	})
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for len(rec.list()) == 0 { // aguarda o início da goroutine
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []string{"worker iniciado", "worker terminou", "pool fechado"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Fatalf("ordem %v, esperada %v", got, want)
	}
}

func TestBackgroundWorkerThatDoesNotStopReturnsDeadlineButReleasesResources(t *testing.T) {
	rec := &recorder{}
	release := make(chan struct{})
	defer close(release)
	app := build(rec, 100*time.Millisecond, func(context.Context) { <-release }) // ignora o cancelamento
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := app.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("esperava erro de prazo excedido, obtido %v", err)
	}
	if got := rec.list(); !slices.Equal(got, []string{"pool fechado"}) {
		t.Fatalf("os demais recursos deveriam ser liberados mesmo assim: %v", got)
	}
}
