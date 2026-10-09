package bootstrap

import (
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	httpadapter "desafio/internal/adapters/http"
	"desafio/internal/app"
	"desafio/internal/platform/config"
	"desafio/internal/platform/metrics"
)

var httpModule = fx.Module("http",
	fx.Provide(newHandler, newServer),
	fx.Invoke(func(*httpadapter.Server) {}), // garante a construção (e os hooks) do servidor
)

type handlerParams struct {
	fx.In

	Log          *slog.Logger
	Cfg          config.Config
	Metrics      *metrics.Metrics
	Wallets      app.Wallets
	Transactions app.Transactions
	Checkers     []httpadapter.Checker `group:"checkers"`
}

func newHandler(p handlerParams) http.Handler {
	return httpadapter.NewHandler(p.Log, p.Metrics.Handler(), p.Checkers, p.Cfg.CheckTimeout,
		httpadapter.Deps{Wallets: p.Wallets, Transactions: p.Transactions}, p.Metrics.Middleware)
}

// newServer cria o servidor e registra o ciclo de vida: o início abre o listener sem bloquear e a
// parada recusa novas conexões e aguarda as em andamento até o prazo.
func newServer(lc fx.Lifecycle, log *slog.Logger, cfg config.Config, h http.Handler) *httpadapter.Server {
	server := httpadapter.NewServer(log, cfg.HTTPAddr, h)
	lc.Append(fx.Hook{OnStart: server.Start, OnStop: server.Stop})
	return server
}
