//go:build integration

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	httpadapter "desafio/internal/adapters/http"
	"desafio/internal/adapters/postgres"
	"desafio/internal/app"
	"desafio/internal/bootstrap"
	"desafio/internal/platform/config"
	"desafio/internal/platform/logging"
)

// syncBuffer recebe os logs JSON (aplicação e eventos do Fx) de várias goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) lines() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(s.b.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			out = append(out, map[string]any{"_invalid": l})
			continue
		}
		out = append(out, m)
	}
	return out
}

func testConfig(t *testing.T, override map[string]string) config.Config {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL não definida")
	}
	env := map[string]string{
		"DATABASE_URL":     os.Getenv("DATABASE_URL"),
		"AWS_REGION":       os.Getenv("AWS_REGION"),
		"AWS_ENDPOINT_URL": os.Getenv("AWS_ENDPOINT_URL"),
		"HTTP_ADDR":        "127.0.0.1:0",
		"SHUTDOWN_TIMEOUT": "5s",
		"CHECK_TIMEOUT":    "2s",
	}
	for k, v := range override {
		env[k] = v
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func ctx10(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// stopOrder devolve a ordem em que os hooks OnStop foram executados, identificados pelo construtor.
func stopOrder(lines []map[string]any) []string {
	var order []string
	for _, l := range lines {
		if l["msg"] == "OnStop hook executed" {
			order = append(order, l["caller"].(string))
		}
	}
	return order
}

func indexOf(order []string, part string) int {
	for i, c := range order {
		if strings.Contains(c, part) {
			return i
		}
	}
	return -1
}

func TestAPIStartServeAndOrderlyStop(t *testing.T) {
	cfg := testConfig(t, nil)
	buf := &syncBuffer{}
	log := logging.New(buf, slog.LevelInfo)

	var srv *httpadapter.Server
	var pool *pgxpool.Pool
	app := fx.New(bootstrap.API(cfg, log), fx.Populate(&srv, &pool))
	if err := app.Start(ctx10(t)); err != nil {
		t.Fatal(err)
	}
	base := "http://" + srv.Addr()

	resp, err := http.Get(base + "/health/ready")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("readiness: %v, %v", resp, err)
	}
	resp.Body.Close()
	create := postJSON(t, base+"/wallets", `{"playerId":"`+newUUID()+`","initialBalance":{"amount":"10.00","currency":"BRL"}}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("abertura de carteira: %d", create.StatusCode)
	}

	if err := app.Stop(ctx10(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// recursos liberados: porta fechada e pool fechado
	if _, err := http.Get(base + "/health/live"); err == nil {
		t.Error("o servidor ainda aceita conexões depois do encerramento")
	}
	if err := pool.Ping(context.Background()); err == nil {
		t.Error("o pool deveria estar fechado")
	}

	// o servidor para antes do fechamento do pool
	lines := buf.lines()
	order := stopOrder(lines)
	srvIdx, poolIdx := indexOf(order, "newServer"), indexOf(order, "newPool")
	if srvIdx < 0 || poolIdx < 0 || srvIdx > poolIdx {
		t.Errorf("ordem de parada inesperada: %v", order)
	}
	// os eventos do Fx saem no mesmo formato JSON da aplicação
	for _, l := range lines {
		if _, bad := l["_invalid"]; bad {
			t.Errorf("linha de log que não é JSON: %v", l["_invalid"])
		}
	}
	if len(order) == 0 {
		t.Error("nenhum evento de parada do Fx registrado")
	}
}

func TestStartFailsFastWhenDatabaseIsUnreachable(t *testing.T) {
	addr := freeAddr(t)
	cfg := testConfig(t, map[string]string{
		"DATABASE_URL": "postgres://app:app@127.0.0.1:1/app?sslmode=disable&connect_timeout=2",
		"HTTP_ADDR":    addr,
	})
	app := fx.New(fx.NopLogger, bootstrap.API(cfg, logging.New(&syncBuffer{}, slog.LevelInfo)))

	err := app.Start(ctx10(t))
	if err == nil {
		_ = app.Stop(context.Background())
		t.Fatal("o início deveria falhar com o PostgreSQL inacessível")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("o erro deveria identificar o PostgreSQL: %v", err)
	}
	// nenhuma porta foi aberta
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Error("o servidor HTTP não deveria estar escutando")
	}
}

func TestWorkerStartsAndReleasesPool(t *testing.T) {
	cfg := testConfig(t, nil)
	buf := &syncBuffer{}
	var pool *pgxpool.Pool
	app := fx.New(bootstrap.Worker(cfg, logging.New(buf, slog.LevelInfo)), fx.Populate(&pool))

	if err := app.Start(ctx10(t)); err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("o worker deveria estar conectado ao banco: %v", err)
	}
	if err := app.Stop(ctx10(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := pool.Ping(context.Background()); err == nil {
		t.Error("o pool do worker deveria estar fechado")
	}
	order := stopOrder(buf.lines())
	loop, poolIdx := indexOf(order, "runInBackground"), indexOf(order, "newPool")
	if loop < 0 || poolIdx < 0 || loop > poolIdx {
		t.Errorf("o laço do worker deveria parar antes do pool: %v", order)
	}
}

// freeAddr reserva uma porta livre de loopback e a devolve, para o teste não depender do servidor para descobri-la.
func freeAddr(t *testing.T) string {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return probe.Addr().String()
}

// Run inicia, espera SIGTERM e encerra com sucesso, liberando a porta.
func TestRunStopsOnSIGTERM(t *testing.T) {
	addr := freeAddr(t)
	cfg := testConfig(t, map[string]string{"HTTP_ADDR": addr})
	app := fx.New(fx.NopLogger, bootstrap.API(cfg, logging.New(&syncBuffer{}, slog.LevelInfo)))
	_ = app.Done() // registra o tratamento de sinais antes de enviar o sinal ao próprio processo

	done := make(chan error, 1)
	go func() { done <- bootstrap.Run(app) }()

	up := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get("http://" + addr + "/health/live"); err == nil {
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !up {
		t.Fatal("a aplicação não ficou disponível")
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run devolveu erro: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a aplicação não encerrou depois do SIGTERM")
	}
	if _, err := http.Get("http://" + addr + "/health/live"); err == nil {
		t.Error("a porta deveria estar fechada")
	}
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = readRandom(b)
	b[6], b[8] = (b[6]&0x0f)|0x40, (b[8]&0x3f)|0x80
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[v>>4], hex[v&0x0f])
	}
	return string(out)
}

// Com o worker em execução, a referência que chega retoma a pendência sem intervenção; a parada encerra o laço
// antes de fechar o pool e registra o término.
func TestWorkerResumesPendingAutomaticallyAndStopsBeforePool(t *testing.T) {
	cfg := testConfig(t, map[string]string{"REFERENCE_POLL_INTERVAL": "50ms"})
	ctx := ctx10(t)

	// a base de testes é compartilhada: encerra pendências deixadas por outros testes
	setup, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close()
	if _, err := setup.Exec(ctx, `UPDATE wager_transactions SET status = 'REJECTED', failure_code = 'TEST_CLEANUP', next_attempt_at = NULL WHERE status = 'PENDING_REFERENCE'`); err != nil {
		t.Fatal(err)
	}

	buf := &syncBuffer{}
	var (
		txs     app.Transactions
		wallets app.Wallets
		pool    *pgxpool.Pool
	)
	fxApp := fx.New(bootstrap.Worker(cfg, logging.New(buf, slog.LevelInfo)), fx.Populate(&txs, &wallets, &pool))
	if err := fxApp.Start(ctx); err != nil {
		t.Fatal(err)
	}

	player := newUUID()
	w, err := wallets.Open(ctx, app.OpenWalletInput{PlayerID: player, InitialBalance: app.Money{Amount: "100.00", Currency: "BRL"}})
	if err != nil {
		t.Fatal(err)
	}
	input := func(kind, ext, ref string) app.SubmitInput {
		in := app.SubmitInput{
			ProviderID: "prov", ExternalTransactionID: ext, PlayerID: player, WalletID: w.ID, RoundID: "r", GameID: "g",
			Kind: kind, Money: app.Money{Amount: "25.00", Currency: "BRL"}, IdempotencyKey: "k-" + ext,
			ReferenceExternalTransactionID: ref,
		}
		return in
	}
	betExt, refundExt := newUUID(), newUUID()
	pending, err := txs.Submit(ctx, input("REFUND", refundExt, betExt))
	if err != nil || pending.Status != "PENDING_REFERENCE" {
		t.Fatalf("reversão antes da referência: %+v, %v", pending, err)
	}
	if res, err := txs.Submit(ctx, input("BET", betExt, "")); err != nil || res.Status != "PROCESSED" {
		t.Fatalf("aposta: %+v, %v", res, err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := txs.GetByExternal(ctx, "prov", refundExt)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "PROCESSED" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a pendência não foi retomada pelo worker (estado %s)", got.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got, _ := wallets.Get(ctx, w.ID); got.Balance.Amount != "100.00" {
		t.Errorf("saldo = %s", got.Balance.Amount)
	}

	if err := fxApp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := pool.Ping(context.Background()); err == nil {
		t.Error("o pool deveria estar fechado")
	}
	lines := buf.lines()
	order := stopOrder(lines)
	loop, poolIdx := indexOf(order, "runInBackground"), indexOf(order, "newPool")
	if loop < 0 || poolIdx < 0 || loop > poolIdx {
		t.Errorf("o laço deveria parar antes do pool: %v", order)
	}
	ended := false
	for _, l := range lines {
		if l["msg"] == "worker de referências pendentes encerrado" {
			ended = true
		}
	}
	if !ended {
		t.Error("o término do worker deveria ser registrado em log")
	}
}
