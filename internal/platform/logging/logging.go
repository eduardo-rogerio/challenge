// Package logging fornece logs JSON estruturados e propagação do logger pelo contexto.
package logging

import (
	"context"
	"io"
	"log/slog"
)

type ctxKey struct{}

func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Into devolve um contexto que carrega o logger.
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From devolve o logger do contexto ou o padrão quando não houver.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
