package httpadapter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"desafio/internal/app"
)

const (
	maxBodyBytes         = 1 << 20
	maxIdempotencyKeyLen = 255

	IdempotencyHeader = "Idempotency-Key"
)

// Validação de formato apenas; regras de negócio pertencem ao domínio.
var (
	uuidRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	amountRE   = regexp.MustCompile(`^[0-9]+\.[0-9]{2}$`)
	currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)
)

// validIdempotencyKey aceita de 1 a 255 caracteres ASCII imprimíveis, sem espaços.
func validIdempotencyKey(k string) bool {
	if k == "" || len(k) > maxIdempotencyKeyLen {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7E {
			return false
		}
	}
	return true
}

func isUUID(s string) bool { return uuidRE.MatchString(s) }

func validMoney(m app.Money) bool {
	return amountRE.MatchString(m.Amount) && currencyRE.MatchString(m.Currency)
}

// decodeJSON lê um único objeto JSON, rejeitando campos desconhecidos e conteúdo extra.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("conteúdo após o objeto JSON")
	}
	return nil
}
