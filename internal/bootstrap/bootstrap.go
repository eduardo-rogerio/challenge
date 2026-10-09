// Package bootstrap compõe a aplicação com Uber Fx. É o único lugar (além de cmd/*) que conhece o Fx:
// domínio, casos de uso e adaptadores continuam com construtores simples.
package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"desafio/internal/platform/config"
)

// checkersGroup é o grupo de valores onde cada dependência registra seu verificador de saúde.
const checkersGroup = `group:"checkers"`

// base fornece a configuração e o logger já criados, envia os eventos do Fx ao mesmo logger JSON
// e aplica o prazo de parada configurado.
func base(cfg config.Config, log *slog.Logger) fx.Option {
	return fx.Options(
		fx.Supply(cfg, log),
		fx.WithLogger(newFxLogger),
		fx.StopTimeout(cfg.ShutdownTimeout),
	)
}

// API é a composição do executável HTTP.
func API(cfg config.Config, log *slog.Logger) fx.Option {
	return fx.Options(
		base(cfg, log),
		databaseModule,
		messagingModule,
		observabilityModule,
		applicationModule,
		httpModule,
	)
}

// Worker é a composição do executável de workers. Os workers futuros (outbox, referências pendentes,
// consumidor SQS) se registram aqui, depois do módulo de banco.
func Worker(cfg config.Config, log *slog.Logger) fx.Option {
	return fx.Options(
		base(cfg, log),
		databaseModule,
		observabilityModule,
		applicationModule,
		pendingReferencesModule,
	)
}

// Run inicia a aplicação, aguarda SIGINT/SIGTERM e a encerra dentro do prazo de parada.
// Falha de início devolve erro (o Fx já desfaz o que foi iniciado).
func Run(app *fx.App) error {
	startCtx, cancel := context.WithTimeout(context.Background(), app.StartTimeout())
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Done()

	stopCtx, cancel := context.WithTimeout(context.Background(), app.StopTimeout()+time.Second)
	defer cancel()
	return app.Stop(stopCtx)
}

// fxLogger envia os eventos do Fx ao logger JSON da aplicação: início, parada e erros em INFO/ERROR;
// o detalhe da injeção (cada construtor fornecido e invocado, com stacktrace) só em DEBUG.
type fxLogger struct{ info, debug *fxevent.SlogLogger }

func newFxLogger(log *slog.Logger) fxevent.Logger {
	debug := &fxevent.SlogLogger{Logger: log}
	debug.UseLogLevel(slog.LevelDebug)
	return fxLogger{info: &fxevent.SlogLogger{Logger: log}, debug: debug}
}

func (l fxLogger) LogEvent(e fxevent.Event) {
	switch e.(type) {
	case *fxevent.Supplied, *fxevent.Provided, *fxevent.Replaced, *fxevent.Decorated,
		*fxevent.BeforeRun, *fxevent.Run, *fxevent.Invoking, *fxevent.Invoked, *fxevent.LoggerInitialized:
		l.debug.LogEvent(e)
	default:
		l.info.LogEvent(e)
	}
}
