package app

import (
	"errors"
	"fmt"
	"time"
)

// ReferencePolicy define como o worker retoma reversões em PENDING_REFERENCE.
type ReferencePolicy struct {
	MaxAttempts  int           // tentativas antes de expirar
	TTL          time.Duration // prazo absoluto desde a criação da pendência
	BaseDelay    time.Duration // atraso depois da primeira tentativa sem sucesso
	MaxDelay     time.Duration // teto do atraso entre tentativas
	PollInterval time.Duration // intervalo entre varreduras do worker
	ClaimLease   time.Duration // por quanto tempo uma pendência reivindicada fica reservada
	BatchSize    int           // pendências reivindicadas por varredura
}

func DefaultReferencePolicy() ReferencePolicy {
	return ReferencePolicy{
		MaxAttempts:  10,
		TTL:          time.Hour,
		BaseDelay:    5 * time.Second,
		MaxDelay:     5 * time.Minute,
		PollInterval: time.Second,
		ClaimLease:   30 * time.Second,
		BatchSize:    20,
	}
}

// Validate rejeita valores não positivos e um teto de atraso menor que o atraso base.
func (p ReferencePolicy) Validate() error {
	var errs []error
	if p.MaxAttempts < 1 {
		errs = append(errs, errors.New("MaxAttempts deve ser positivo"))
	}
	if p.BatchSize < 1 {
		errs = append(errs, errors.New("BatchSize deve ser positivo"))
	}
	for name, d := range map[string]time.Duration{
		"TTL": p.TTL, "BaseDelay": p.BaseDelay, "MaxDelay": p.MaxDelay, "PollInterval": p.PollInterval, "ClaimLease": p.ClaimLease,
	} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("%s deve ser positivo", name))
		}
	}
	if p.MaxDelay < p.BaseDelay {
		errs = append(errs, errors.New("MaxDelay não pode ser menor que BaseDelay"))
	}
	return errors.Join(errs...)
}

// JitterFactor converte um sorteio r em [0,1) em um fator em [0,8; 1,2).
func JitterFactor(r float64) float64 { return 0.8 + 0.4*r }

// Backoff devolve o atraso depois da tentativa de número attempts (1 = primeira sem sucesso):
// base × 2^(attempts−1), limitado a MaxDelay, multiplicado por jitter e limitado de novo.
func (p ReferencePolicy) Backoff(attempts int, jitter float64) time.Duration {
	d := p.BaseDelay
	for i := 1; i < attempts && d < p.MaxDelay; i++ {
		d *= 2
	}
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	d = time.Duration(float64(d) * jitter)
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	return d
}

// Exhausted informa se uma pendência deve expirar: máximo de tentativas atingido ou TTL vencido.
func (p ReferencePolicy) Exhausted(attempts int, createdAt, now time.Time) bool {
	return attempts >= p.MaxAttempts || !now.Before(createdAt.Add(p.TTL))
}
