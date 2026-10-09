package httpadapter

import (
	"errors"
	"net/http"

	"desafio/internal/app"
	"desafio/internal/platform/logging"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

func writeInvalid(w http.ResponseWriter, message string) {
	writeJSONError(w, http.StatusBadRequest, "INVALID_REQUEST", message)
}

// writeError traduz um erro do caso de uso; erros desconhecidos viram 500 sem expor detalhes.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "INVALID_REQUEST", "entrada inválida")
	case errors.Is(err, app.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "recurso não encontrado")
	case errors.Is(err, app.ErrIdempotencyConflict):
		writeJSONError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "chave ou operação já usada com conteúdo diferente")
	case errors.Is(err, app.ErrInProgress):
		writeJSONError(w, http.StatusConflict, "REQUEST_IN_PROGRESS", "requisição com a mesma chave em andamento")
	case errors.Is(err, app.ErrConflict):
		writeJSONError(w, http.StatusConflict, "CONFLICT", "conflito com o estado atual")
	case errors.Is(err, app.ErrUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "serviço temporariamente indisponível")
	case errors.Is(err, app.ErrNotImplemented):
		writeJSONError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "funcionalidade ainda não implementada")
	default:
		logging.From(r.Context()).Error("erro interno", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "INTERNAL", "erro interno")
	}
}
