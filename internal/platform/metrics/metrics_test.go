package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareRecordsRequests(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	mux.Handle("GET /metrics", m.Handler())
	h := m.Middleware(mux)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`http_requests_total{method="GET",route="GET /ping",status="202"} 1`,
		`http_request_duration_seconds_count{method="GET",route="GET /ping"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("métrica ausente: %s", want)
		}
	}
}

func TestReconciliationDivergenceCounter(t *testing.T) {
	m := New()
	m.ReconciliationDivergence()
	m.ReconciliationDivergence()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "wallet_reconciliation_divergences_total 2") {
		t.Errorf("contador ausente:\n%s", rec.Body.String())
	}
}

func TestReferenceOutcomeCounter(t *testing.T) {
	m := New()
	for _, o := range []string{"processed", "retry", "retry", "expired"} {
		m.ReferenceOutcome(o)
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`pending_reference_outcomes_total{outcome="processed"} 1`,
		`pending_reference_outcomes_total{outcome="retry"} 2`,
		`pending_reference_outcomes_total{outcome="expired"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("métrica ausente: %s", want)
		}
	}
}
