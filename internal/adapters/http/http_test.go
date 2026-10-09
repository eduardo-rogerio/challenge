package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeChecker struct {
	name string
	err  error
}

func (f fakeChecker) Name() string                { return f.name }
func (f fakeChecker) Check(context.Context) error { return f.err }

func handler(buf io.Writer, checkers ...Checker) http.Handler {
	log := slog.New(slog.NewJSONHandler(buf, nil))
	return NewHandler(log, http.NotFoundHandler(), checkers, time.Second, Deps{})
}

func TestLive(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(io.Discard, fakeChecker{"postgres", errors.New("down")}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("live = %d", rec.Code)
	}
}

func TestReadyOK(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(io.Discard, fakeChecker{"postgres", nil}, fakeChecker{"sqs", nil}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready = %d", rec.Code)
	}
}

func TestReadyIdentifiesFailingDependency(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(io.Discard, fakeChecker{"postgres", errors.New("down")}, fakeChecker{"sqs", nil}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready = %d, esperado 503", rec.Code)
	}
	var body readyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Checks["postgres"] != "error" || body.Checks["sqs"] != "ok" {
		t.Fatalf("checks inesperados: %+v", body.Checks)
	}
}

func TestCorrelationIDGeneratedAndLogged(t *testing.T) {
	var buf bytes.Buffer
	rec := httptest.NewRecorder()
	handler(&buf).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	id := rec.Header().Get(CorrelationHeader)
	if id == "" {
		t.Fatal("header de correlação ausente")
	}
	if !strings.Contains(buf.String(), `"correlationId":"`+id+`"`) {
		t.Fatalf("log sem correlationId %q: %s", id, buf.String())
	}
}

func TestCorrelationIDPropagated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	req.Header.Set(CorrelationHeader, "abc-123")
	rec := httptest.NewRecorder()
	handler(io.Discard).ServeHTTP(rec, req)
	if got := rec.Header().Get(CorrelationHeader); got != "abc-123" {
		t.Fatalf("correlationId = %q", got)
	}
}

func TestShutdownWaitsForInflightRequest(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(log, "127.0.0.1:0", mux)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	url := "http://" + srv.Addr()

	result := make(chan int, 1)
	go func() {
		resp, err := http.Get(url + "/slow")
		if err != nil {
			result <- -1
			return
		}
		resp.Body.Close()
		result <- resp.StatusCode
	}()
	<-started

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- srv.Stop(ctx)
	}()

	// Novas conexões são recusadas assim que o shutdown começa.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c := &http.Client{Timeout: 200 * time.Millisecond}
		resp, err := c.Get(url + "/slow")
		if err != nil {
			break
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("servidor continuou aceitando conexões durante o shutdown")
		}
	}

	close(release)
	if code := <-result; code != http.StatusOK {
		t.Fatalf("requisição em andamento terminou com %d", code)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
