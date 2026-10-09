package app

import (
	"context"
	"log/slog"
	"time"
)

// batchResolver é o que o laço precisa do resolvedor: uma varredura que devolve quantas pendências reivindicou.
type batchResolver interface {
	ResolveBatch(ctx context.Context) (int, error)
}

// PendingReferenceWorker executa varreduras periódicas do resolvedor até o contexto ser cancelado.
type PendingReferenceWorker struct {
	resolver batchResolver
	interval time.Duration
	batch    int
	log      *slog.Logger
}

func NewPendingReferenceWorker(resolver *PendingReferenceResolver, policy ReferencePolicy, log *slog.Logger) *PendingReferenceWorker {
	return &PendingReferenceWorker{resolver: resolver, interval: policy.PollInterval, batch: policy.BatchSize, log: log}
}

// Run bloqueia até ctx ser cancelado. Enquanto as varreduras vierem com lote cheio, continua sem esperar;
// do contrário espera o intervalo. Erros (como indisponibilidade do banco) são registrados e a varredura
// seguinte tenta de novo; o cancelamento é a única forma de terminar.
func (w *PendingReferenceWorker) Run(ctx context.Context) {
	w.log.Info("worker de referências pendentes iniciado", "interval", w.interval.String())
	defer w.log.Info("worker de referências pendentes encerrado")

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		next := w.interval
		n, err := w.resolver.ResolveBatch(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			w.log.Error("varredura de referências pendentes falhou", "error", err)
		case err == nil && n >= w.batch:
			next = 0 // lote cheio: pode haver mais trabalho
		}
		timer.Reset(next)
	}
}
