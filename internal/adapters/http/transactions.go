package httpadapter

import (
	"net/http"

	"desafio/internal/app"
)

// Tipos aceitos nas entradas externas; OPENING é reservado à abertura interna de carteira.
var externalKinds = map[string]bool{"BET": true, "WIN": true, "LOSS": true, "REFUND": true, "ROLLBACK": true}

type transactionHandlers struct{ svc app.Transactions }

func registerTransactionRoutes(mux *http.ServeMux, svc app.Transactions) {
	h := transactionHandlers{svc}
	mux.HandleFunc("POST /wagering/transactions", h.submit)
	mux.HandleFunc("GET /wagering/transactions/{transactionId}", h.get)
	mux.HandleFunc("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", h.getByExternal)
}

// validateSubmit confere apenas o formato; devolve a mensagem do primeiro problema ou "".
func validateSubmit(in app.SubmitInput) string {
	switch {
	case in.ProviderID == "" || in.ExternalTransactionID == "" || in.RoundID == "" || in.GameID == "":
		return "providerId, externalTransactionId, roundId e gameId são obrigatórios"
	case !isUUID(in.PlayerID) || !isUUID(in.WalletID):
		return "playerId e walletId devem ser UUIDs"
	case !externalKinds[in.Kind]:
		return "kind deve ser BET, WIN, LOSS, REFUND ou ROLLBACK"
	case !validMoney(in.Money):
		return "money inválido"
	case (in.Kind == "REFUND" || in.Kind == "ROLLBACK") && in.ReferenceExternalTransactionID == "":
		return "referenceExternalTransactionId é obrigatório para REFUND e ROLLBACK"
	}
	return ""
}

func (h transactionHandlers) submit(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(IdempotencyHeader)
	if !validIdempotencyKey(key) {
		writeInvalid(w, "header Idempotency-Key é obrigatório (1 a 255 caracteres ASCII imprimíveis, sem espaços)")
		return
	}
	var in app.SubmitInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeInvalid(w, "corpo inválido")
		return
	}
	in.IdempotencyKey = key
	if msg := validateSubmit(in); msg != "" {
		writeInvalid(w, msg)
		return
	}
	res, err := h.svc.Submit(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, submitStatusCode(res.Status), res)
}

// submitStatusCode converte o estado da transação em código HTTP.
func submitStatusCode(status string) int {
	switch status {
	case app.StatusProcessed:
		return http.StatusOK
	case app.StatusPending, app.StatusPendingReference:
		return http.StatusAccepted
	case app.StatusRejected:
		return http.StatusUnprocessableEntity
	default: // FAILED e qualquer estado desconhecido
		return http.StatusInternalServerError
	}
}

func (h transactionHandlers) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("transactionId")
	if !isUUID(id) {
		writeInvalid(w, "transactionId deve ser um UUID")
		return
	}
	tx, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

func (h transactionHandlers) getByExternal(w http.ResponseWriter, r *http.Request) {
	tx, err := h.svc.GetByExternal(r.Context(), r.PathValue("providerId"), r.PathValue("externalTransactionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tx)
}
