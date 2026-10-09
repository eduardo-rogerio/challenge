package app

import (
	"strings"
	"testing"
	"time"
)

func testPolicy() ReferencePolicy {
	return ReferencePolicy{
		MaxAttempts: 5, TTL: time.Hour, BaseDelay: time.Second, MaxDelay: 10 * time.Second,
		PollInterval: time.Second, ClaimLease: 30 * time.Second, BatchSize: 10,
	}
}

func TestDefaultPolicyIsValid(t *testing.T) {
	if err := DefaultReferencePolicy().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	p := testPolicy()
	want := []time.Duration{1, 2, 4, 8, 10, 10, 10} // segundos; o teto é 10s
	prev := time.Duration(0)
	for i, w := range want {
		got := p.Backoff(i+1, 1.0)
		if got != w*time.Second {
			t.Errorf("tentativa %d: %v, esperado %v", i+1, got, w*time.Second)
		}
		if got < prev {
			t.Errorf("o atraso diminuiu na tentativa %d", i+1)
		}
		prev = got
	}
}

func TestBackoffJitterStaysWithinBounds(t *testing.T) {
	p := testPolicy()
	for attempts := 1; attempts <= 8; attempts++ {
		base := p.Backoff(attempts, 1.0)
		for _, r := range []float64{0, 0.25, 0.5, 0.999999} {
			got := p.Backoff(attempts, JitterFactor(r))
			if got > p.MaxDelay {
				t.Errorf("tentativa %d, r=%v: %v excede o teto %v", attempts, r, got, p.MaxDelay)
			}
			if got < time.Duration(float64(base)*0.8)-time.Nanosecond {
				t.Errorf("tentativa %d, r=%v: %v abaixo do mínimo do jitter", attempts, r, got)
			}
		}
	}
	if f := JitterFactor(0); f != 0.8 {
		t.Errorf("fator mínimo = %v", f)
	}
}

func TestBackoffHugeAttemptsDoesNotOverflow(t *testing.T) {
	p := testPolicy()
	if got := p.Backoff(1_000_000, 1.2); got != p.MaxDelay {
		t.Fatalf("got %v", got)
	}
}

func TestExhausted(t *testing.T) {
	p := testPolicy()
	created := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		attempts int
		now      time.Time
		want     bool
	}{
		{"dentro dos limites", 1, created.Add(time.Minute), false},
		{"última tentativa antes do máximo", 4, created.Add(time.Minute), false},
		{"máximo de tentativas", 5, created.Add(time.Minute), true},
		{"TTL vencido com tentativas sobrando", 1, created.Add(time.Hour), true},
		{"um instante antes do TTL", 1, created.Add(time.Hour - time.Nanosecond), false},
	}
	for _, tt := range tests {
		if got := p.Exhausted(tt.attempts, created, tt.now); got != tt.want {
			t.Errorf("%s: %v", tt.name, got)
		}
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]func(*ReferencePolicy){
		"MaxAttempts":  func(p *ReferencePolicy) { p.MaxAttempts = 0 },
		"BatchSize":    func(p *ReferencePolicy) { p.BatchSize = 0 },
		"TTL":          func(p *ReferencePolicy) { p.TTL = 0 },
		"BaseDelay":    func(p *ReferencePolicy) { p.BaseDelay = -time.Second },
		"PollInterval": func(p *ReferencePolicy) { p.PollInterval = 0 },
		"ClaimLease":   func(p *ReferencePolicy) { p.ClaimLease = 0 },
		"MaxDelay":     func(p *ReferencePolicy) { p.MaxDelay = p.BaseDelay - 1 },
	}
	for field, mutate := range bad {
		p := testPolicy()
		mutate(&p)
		err := p.Validate()
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: erro %v", field, err)
		}
	}
}
