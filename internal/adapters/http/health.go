// Package httpadapter contém o servidor HTTP, middlewares e health checks.
package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Checker verifica a disponibilidade de uma dependência externa.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

type readyResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func liveHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyHandler executa os checkers em paralelo e responde 503 se algum falhar.
func readyHandler(checkers []Checker, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		var (
			mu     sync.Mutex
			wg     sync.WaitGroup
			checks = make(map[string]string, len(checkers))
		)
		for _, c := range checkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := "ok"
				if err := c.Check(ctx); err != nil {
					res = "error"
				}
				mu.Lock()
				checks[c.Name()] = res
				mu.Unlock()
			}()
		}
		wg.Wait()

		status, code := "ok", http.StatusOK
		names := make([]string, 0, len(checks))
		for n := range checks {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if checks[n] != "ok" {
				status, code = "unavailable", http.StatusServiceUnavailable
			}
		}
		writeJSON(w, code, readyResponse{Status: status, Checks: checks})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
