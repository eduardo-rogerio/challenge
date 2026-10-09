package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	httpadapter "desafio/internal/adapters/http"
	"desafio/internal/app"
	"desafio/internal/platform/config"
	"desafio/internal/platform/logging"
	"desafio/internal/platform/metrics"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	env := map[string]string{
		"DATABASE_URL": "postgres://app:app@127.0.0.1:1/app?sslmode=disable",
		"AWS_REGION":   "us-east-1",
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func testLogger() *slog.Logger { return logging.New(io.Discard, slog.LevelInfo) }

// ValidateApp confere o grafo de dependências sem executar construtores nem tocar a infraestrutura.
func TestGraphsAreValid(t *testing.T) {
	cfg, log := testConfig(t), testLogger()
	cases := map[string]fx.Option{
		"API":         API(cfg, log),
		"Worker":      Worker(cfg, log),
		"database":    fx.Options(base(cfg, log), databaseModule),
		"messaging":   fx.Options(base(cfg, log), messagingModule),
		"application": fx.Options(base(cfg, log), databaseModule, observabilityModule, applicationModule),
	}
	for name, opt := range cases {
		if err := fx.ValidateApp(opt); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// O teste só tem valor se um grafo incompleto for de fato rejeitado.
func TestIncompleteGraphIsRejected(t *testing.T) {
	cfg, log := testConfig(t), testLogger()
	if err := fx.ValidateApp(base(cfg, log), applicationModule, httpModule); err == nil {
		t.Fatal("grafo sem o módulo de banco deveria ser rejeitado")
	}
}

type nopWallets struct{ app.Wallets }
type nopTransactions struct{ app.Transactions }

type staticChecker struct {
	name string
	err  error
}

func (c staticChecker) Name() string                { return c.name }
func (c staticChecker) Check(context.Context) error { return c.err }

// Um verificador registrado no grupo passa a fazer parte do readiness sem alterar o servidor.
func TestCheckerGroupFeedsReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"saudável", nil, http.StatusOK},
		{"com falha", errors.New("fora do ar"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, log := testConfig(t), testLogger()
			var h http.Handler
			fxApp := fx.New(
				fx.NopLogger,
				fx.Supply(cfg, log),
				fx.Provide(
					newHandler,
					metrics.New,
					func() app.Wallets { return nopWallets{} },
					func() app.Transactions { return nopTransactions{} },
					fx.Annotate(func() httpadapter.Checker { return staticChecker{"extra", tc.err} }, fx.ResultTags(checkersGroup)),
				),
				fx.Populate(&h),
			)
			if err := fxApp.Err(); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			var body struct {
				Checks map[string]string `json:"checks"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body.Checks["extra"]; !ok || rec.Code != tc.status {
				t.Fatalf("status %d, checks %v", rec.Code, body.Checks)
			}
		})
	}
}

// Início e parada aparecem em INFO; o detalhe da injeção só em DEBUG; erros sempre.
func TestFxLoggerLevels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		level slog.Level
		event fxevent.Event
		want  bool
	}{
		{"parada em info", slog.LevelInfo, &fxevent.OnStopExecuted{FunctionName: "f", CallerName: "c"}, true},
		{"início em info", slog.LevelInfo, &fxevent.OnStartExecuted{FunctionName: "f", CallerName: "c"}, true},
		{"construtor em info", slog.LevelInfo, &fxevent.Provided{ConstructorName: "c", OutputTypeNames: []string{"T"}}, false},
		{"fornecido em info", slog.LevelInfo, &fxevent.Supplied{TypeName: "t"}, false},
		{"construtor em debug", slog.LevelDebug, &fxevent.Provided{ConstructorName: "c", OutputTypeNames: []string{"T"}}, true},
		{"erro de invocação em info", slog.LevelInfo, &fxevent.Invoked{FunctionName: "f", Err: errors.New("falhou")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			newFxLogger(logging.New(&buf, tc.level)).LogEvent(tc.event)
			if got := buf.Len() > 0; got != tc.want {
				t.Fatalf("registrado=%v, esperado %v (%q)", got, tc.want, buf.String())
			}
		})
	}
}
