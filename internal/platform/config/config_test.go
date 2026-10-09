package config

import (
	"strings"
	"testing"
	"time"

	"desafio/internal/app"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func valid() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://x",
		"AWS_REGION":   "us-east-1",
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.ShutdownTimeout != 20*time.Second || c.SQSQueue != "wager-transactions.fifo" || c.IdempotencyLease != 30*time.Second {
		t.Fatalf("defaults inesperados: %+v", c)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	m := valid()
	delete(m, "DATABASE_URL")
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("esperava erro nomeando DATABASE_URL, obtido %v", err)
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	m := valid()
	m["SHUTDOWN_TIMEOUT"] = "abc"
	m["CHECK_TIMEOUT"] = "-1s"
	m["IDEMPOTENCY_LEASE"] = "0s"
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") || !strings.Contains(err.Error(), "CHECK_TIMEOUT") || !strings.Contains(err.Error(), "IDEMPOTENCY_LEASE") {
		t.Fatalf("esperava reportar os dois campos, obtido %v", err)
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	m := valid()
	m["LOG_LEVEL"] = "loud"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("esperava erro de LOG_LEVEL, obtido %v", err)
	}
}

func TestLoadReferencePolicyDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Reference != app.DefaultReferencePolicy() {
		t.Fatalf("padrões inesperados: %+v", c.Reference)
	}
}

func TestLoadReferencePolicyOverrides(t *testing.T) {
	m := valid()
	m["REFERENCE_MAX_ATTEMPTS"] = "3"
	m["REFERENCE_TTL"] = "10m"
	m["REFERENCE_BASE_DELAY"] = "1s"
	m["REFERENCE_MAX_DELAY"] = "30s"
	m["REFERENCE_POLL_INTERVAL"] = "250ms"
	m["REFERENCE_CLAIM_LEASE"] = "5s"
	m["REFERENCE_BATCH_SIZE"] = "7"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Reference
	if p.MaxAttempts != 3 || p.TTL != 10*time.Minute || p.BaseDelay != time.Second || p.MaxDelay != 30*time.Second ||
		p.PollInterval != 250*time.Millisecond || p.ClaimLease != 5*time.Second || p.BatchSize != 7 {
		t.Fatalf("política: %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReferencePolicyInvalid(t *testing.T) {
	for key, value := range map[string]string{
		"REFERENCE_MAX_ATTEMPTS":  "0",
		"REFERENCE_BATCH_SIZE":    "abc",
		"REFERENCE_TTL":           "-1m",
		"REFERENCE_BASE_DELAY":    "nada",
		"REFERENCE_POLL_INTERVAL": "0s",
		"REFERENCE_CLAIM_LEASE":   "0",
	} {
		m := valid()
		m[key] = value
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%s: erro %v", key, value, err)
		}
	}
	m := valid()
	m["REFERENCE_BASE_DELAY"] = "1m"
	m["REFERENCE_MAX_DELAY"] = "10s"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "REFERENCE_MAX_DELAY") {
		t.Errorf("teto menor que a base: %v", err)
	}
}
