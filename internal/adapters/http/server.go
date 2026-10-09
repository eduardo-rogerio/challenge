package httpadapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"desafio/internal/app"
	"desafio/internal/platform/logging"
)

const CorrelationHeader = "X-Correlation-Id"

// Middleware envolve um handler.
type Middleware func(http.Handler) http.Handler

// Deps reúne as portas usadas pelos handlers de negócio.
type Deps struct {
	Wallets      app.Wallets
	Transactions app.Transactions
}

// NewHandler monta as rotas de health, métricas e de negócio. Os middlewares recebidos ficam
// logo acima do mux, para enxergarem o padrão da rota; correlação e log de requisição os envolvem.
func NewHandler(log *slog.Logger, metricsHandler http.Handler, checkers []Checker, checkTimeout time.Duration, deps Deps, mws ...Middleware) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", liveHandler)
	mux.HandleFunc("GET /health/ready", readyHandler(checkers, checkTimeout))
	mux.Handle("GET /metrics", metricsHandler)
	registerWalletRoutes(mux, deps.Wallets)
	registerTransactionRoutes(mux, deps.Transactions)

	var h http.Handler = mux
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return correlation(log, requestLog(h))
}

// correlation garante um correlationId por requisição, devolvido no header da resposta,
// e coloca no contexto um logger que o inclui.
func correlation(base *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(CorrelationHeader)
		if id == "" {
			id = newID()
		}
		w.Header().Set(CorrelationHeader, id)
		ctx := logging.Into(r.Context(), base.With("correlationId", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type codeWriter struct {
	http.ResponseWriter
	code int
}

func (c *codeWriter) WriteHeader(code int) {
	c.code = code
	c.ResponseWriter.WriteHeader(code)
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		cw := &codeWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(cw, r)
		logging.From(r.Context()).Info("requisição",
			"method", r.Method, "path", r.URL.Path, "status", cw.code,
			"durationMs", time.Since(start).Milliseconds())
	})
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Server encapsula http.Server com início não bloqueante e shutdown gracioso.
type Server struct {
	srv  *http.Server
	addr string
	ln   net.Listener
	log  *slog.Logger
}

func NewServer(log *slog.Logger, addr string, h http.Handler) *Server {
	return &Server{
		log:  log,
		addr: addr,
		srv:  &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second},
	}
}

// Start abre o listener e serve em segundo plano.
func (s *Server) Start(context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.ln = ln
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("servidor HTTP falhou", "error", err)
		}
	}()
	return nil
}

// Addr devolve o endereço efetivo após Start.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Stop recusa novas conexões e aguarda as requisições em andamento até o prazo de ctx;
// vencido o prazo, fecha as conexões restantes.
func (s *Server) Stop(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if err != nil {
		s.log.Warn("prazo de shutdown excedido; fechando conexões", "error", err)
		_ = s.srv.Close()
	}
	return err
}
