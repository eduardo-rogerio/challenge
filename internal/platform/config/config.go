// Package config carrega e valida a configuração a partir de variáveis de ambiente.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"desafio/internal/app"
)

type Config struct {
	HTTPAddr        string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	CheckTimeout    time.Duration
	// IdempotencyLease é o prazo de uma execução em andamento antes de poder ser retomada.
	IdempotencyLease time.Duration

	// Reference é a política do worker de referências pendentes.
	Reference app.ReferencePolicy

	DatabaseURL string

	AWSRegion   string
	SQSEndpoint string // vazio usa o endpoint padrão da AWS
	SQSQueue    string
}

// Load lê a configuração usando getenv e reporta todos os campos inválidos de uma vez.
func Load(getenv func(string) string) (Config, error) {
	l := loader{getenv: getenv}
	c := Config{
		HTTPAddr:         l.str("HTTP_ADDR", ":8080"),
		LogLevel:         l.level("LOG_LEVEL", "info"),
		ShutdownTimeout:  l.duration("SHUTDOWN_TIMEOUT", "20s"),
		CheckTimeout:     l.duration("CHECK_TIMEOUT", "2s"),
		IdempotencyLease: l.duration("IDEMPOTENCY_LEASE", "30s"),
		DatabaseURL:      l.required("DATABASE_URL"),
		AWSRegion:        l.required("AWS_REGION"),
		SQSEndpoint:      l.str("AWS_ENDPOINT_URL", ""),
		SQSQueue:         l.str("SQS_QUEUE_NAME", "wager-transactions.fifo"),
	}
	c.Reference = l.referencePolicy()
	if err := errors.Join(l.errs...); err != nil {
		return Config{}, fmt.Errorf("configuração inválida: %w", err)
	}
	return c, nil
}

type loader struct {
	getenv func(string) string
	errs   []error
}

func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(l.getenv(key)); v != "" {
		return v
	}
	return def
}

func (l *loader) required(key string) string {
	v := strings.TrimSpace(l.getenv(key))
	if v == "" {
		l.errs = append(l.errs, fmt.Errorf("%s é obrigatória", key))
	}
	return v
}

func (l *loader) duration(key, def string) time.Duration {
	raw := l.str(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		l.errs = append(l.errs, fmt.Errorf("%s=%q não é uma duração positiva válida", key, raw))
		return 0
	}
	return d
}

func (l *loader) level(key, def string) slog.Level {
	raw := l.str(key, def)
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(raw)); err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s=%q não é um nível de log válido", key, raw))
	}
	return lv
}

func (l *loader) referencePolicy() app.ReferencePolicy {
	d := app.DefaultReferencePolicy()
	p := app.ReferencePolicy{
		MaxAttempts:  l.integer("REFERENCE_MAX_ATTEMPTS", d.MaxAttempts),
		TTL:          l.durationOr("REFERENCE_TTL", d.TTL),
		BaseDelay:    l.durationOr("REFERENCE_BASE_DELAY", d.BaseDelay),
		MaxDelay:     l.durationOr("REFERENCE_MAX_DELAY", d.MaxDelay),
		PollInterval: l.durationOr("REFERENCE_POLL_INTERVAL", d.PollInterval),
		ClaimLease:   l.durationOr("REFERENCE_CLAIM_LEASE", d.ClaimLease),
		BatchSize:    l.integer("REFERENCE_BATCH_SIZE", d.BatchSize),
	}
	if p.MaxDelay < p.BaseDelay && p.MaxDelay > 0 && p.BaseDelay > 0 {
		l.errs = append(l.errs, errors.New("REFERENCE_MAX_DELAY não pode ser menor que REFERENCE_BASE_DELAY"))
	}
	return p
}

// integer lê um inteiro positivo.
func (l *loader) integer(key string, def int) int {
	raw := strings.TrimSpace(l.getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		l.errs = append(l.errs, fmt.Errorf("%s=%q não é um inteiro positivo", key, raw))
		return 0
	}
	return n
}

// durationOr lê uma duração positiva a partir de um valor padrão.
func (l *loader) durationOr(key string, def time.Duration) time.Duration {
	return l.duration(key, def.String())
}
