// Package metrics expõe métricas Prometheus da aplicação.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
	diverged prometheus.Counter
	outcomes *prometheus.CounterVec
}

func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total de requisições HTTP.",
		}, []string{"method", "route", "status"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Latência das requisições HTTP.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pending_reference_outcomes_total",
			Help: "Desfechos das tentativas do worker de referências pendentes.",
		}, []string{"outcome"}),
		diverged: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wallet_reconciliation_divergences_total",
			Help: "Reconciliações em que o saldo armazenado difere do ledger.",
		}),
	}
	m.reg.MustRegister(m.requests, m.latency, m.diverged, m.outcomes, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// ReconciliationDivergence conta uma divergência entre saldo e ledger.
func (m *Metrics) ReconciliationDivergence() { m.diverged.Inc() }

// ReferenceOutcome conta um desfecho do worker de referências pendentes (processed, rejected, retry, expired).
func (m *Metrics) ReferenceOutcome(outcome string) { m.outcomes.WithLabelValues(outcome).Inc() }

func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Middleware registra contagem e latência por rota (padrão do ServeMux, para limitar a cardinalidade).
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		m.latency.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
