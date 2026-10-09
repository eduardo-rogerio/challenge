package httpadapter

import (
	"net/http"
	"strconv"

	"desafio/internal/app"
)

const (
	defaultLedgerLimit = 50
	maxLedgerLimit     = 100
)

type walletHandlers struct{ svc app.Wallets }

func registerWalletRoutes(mux *http.ServeMux, svc app.Wallets) {
	h := walletHandlers{svc}
	mux.HandleFunc("POST /wallets", h.open)
	mux.HandleFunc("GET /wallets/{walletId}", h.get)
	mux.HandleFunc("GET /wallets/{walletId}/ledger", h.ledger)
	mux.HandleFunc("POST /wallets/{walletId}/reconciliation", h.reconcile)
}

func (h walletHandlers) open(w http.ResponseWriter, r *http.Request) {
	var in app.OpenWalletInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeInvalid(w, "corpo inválido")
		return
	}
	if !isUUID(in.PlayerID) {
		writeInvalid(w, "playerId deve ser um UUID")
		return
	}
	if !validMoney(in.InitialBalance) {
		writeInvalid(w, "initialBalance inválido")
		return
	}
	wallet, err := h.svc.Open(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wallet)
}

func (h walletHandlers) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("walletId")
	if !isUUID(id) {
		writeInvalid(w, "walletId deve ser um UUID")
		return
	}
	wallet, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wallet)
}

func (h walletHandlers) ledger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("walletId")
	if !isUUID(id) {
		writeInvalid(w, "walletId deve ser um UUID")
		return
	}
	limit := defaultLedgerLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLedgerLimit {
			writeInvalid(w, "limit deve ser um inteiro entre 1 e 100")
			return
		}
		limit = n
	}
	page, err := h.svc.Ledger(r.Context(), id, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Entries == nil {
		page.Entries = []app.LedgerEntry{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h walletHandlers) reconcile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("walletId")
	if !isUUID(id) {
		writeInvalid(w, "walletId deve ser um UUID")
		return
	}
	rec, err := h.svc.Reconcile(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
