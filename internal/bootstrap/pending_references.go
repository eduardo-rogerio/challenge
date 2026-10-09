package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"desafio/internal/app"
	"desafio/internal/platform/config"
	"desafio/internal/platform/metrics"
)

var pendingReferencesModule = fx.Module("pending-references",
	fx.Provide(newReferencePolicy, newReferenceResolver, newReferenceWorker),
	fx.Invoke(registerReferenceWorker),
)

func newReferencePolicy(cfg config.Config) app.ReferencePolicy { return cfg.Reference }

func newReferenceResolver(store app.Store, ids app.IDGenerator, clk app.Clock, policy app.ReferencePolicy,
	log *slog.Logger, m *metrics.Metrics) *app.PendingReferenceResolver {
	return app.NewPendingReferenceResolver(store, ids, clk, policy,
		app.WithResolverLogger(log), app.WithOutcomeObserver(m.ReferenceOutcome))
}

func newReferenceWorker(resolver *app.PendingReferenceResolver, policy app.ReferencePolicy, log *slog.Logger) *app.PendingReferenceWorker {
	return app.NewPendingReferenceWorker(resolver, policy, log)
}

func registerReferenceWorker(lc fx.Lifecycle, w *app.PendingReferenceWorker, log *slog.Logger) {
	runInBackground(lc, "referências pendentes", log, w.Run)
}

// runInBackground executa run em uma goroutine iniciada no OnStart. No OnStop cancela o contexto e aguarda o
// término até (90% d)o prazo da parada: o término é observável e acontece antes de qualquer dependência
// registrada antes (como o pool) ser fechada. Se não terminar, o OnStop devolve erro e os demais hooks ainda rodam.
func runInBackground(lc fx.Lifecycle, name string, log *slog.Logger, run func(context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				run(ctx)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			// O Fx não executa os hooks seguintes depois que o contexto de parada expira. Por isso o worker espera
			// só até uma parte do prazo, reservando 10% para os demais hooks (como o fechamento do pool).
			wait := stopCtx
			if deadline, ok := stopCtx.Deadline(); ok {
				var release context.CancelFunc
				wait, release = context.WithDeadline(stopCtx, deadline.Add(-time.Until(deadline)/10))
				defer release()
			}
			select {
			case <-done:
				return nil
			case <-wait.Done():
				log.Error("worker não terminou no prazo", "worker", name)
				return fmt.Errorf("worker %q não terminou no prazo: %w", name, wait.Err())
			}
		},
	})
}
