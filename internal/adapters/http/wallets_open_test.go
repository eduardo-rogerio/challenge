package httpadapter

import (
	"net/http"
	"testing"

	"desafio/internal/app"
)

const openBody = `{"playerId":"` + testUUID + `","initialBalance":{"amount":"1000.00","currency":"BRL"}}`

func TestOpenWallet(t *testing.T) {
	svc := &fakeWallets{wallet: app.Wallet{
		ID: testUUID2, PlayerID: testUUID, Balance: app.Money{Amount: "1000.00", Currency: "BRL"}, Version: 1,
	}}
	rec := do(newTestHandler(svc, nil), http.MethodPost, "/wallets", openBody)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[app.Wallet](t, rec)
	if got.ID != testUUID2 || got.Balance.Amount != "1000.00" || got.Version != 1 {
		t.Fatalf("resposta inesperada: %+v", got)
	}
	if svc.calls != 1 || svc.openIn.PlayerID != testUUID || svc.openIn.InitialBalance.Currency != "BRL" {
		t.Fatalf("chamada inesperada ao caso de uso: %+v (calls=%d)", svc.openIn, svc.calls)
	}
}

func TestOpenWalletInvalidInput(t *testing.T) {
	bodies := map[string]string{
		"json inválido":      `{`,
		"campo desconhecido": `{"playerId":"` + testUUID + `","initialBalance":{"amount":"1.00","currency":"BRL"},"x":1}`,
		"playerId":           `{"playerId":"abc","initialBalance":{"amount":"1.00","currency":"BRL"}}`,
		"amount sem escala":  `{"playerId":"` + testUUID + `","initialBalance":{"amount":"10","currency":"BRL"}}`,
		"amount negativo":    `{"playerId":"` + testUUID + `","initialBalance":{"amount":"-1.00","currency":"BRL"}}`,
		"amount numérico":    `{"playerId":"` + testUUID + `","initialBalance":{"amount":10.00,"currency":"BRL"}}`,
		"moeda minúscula":    `{"playerId":"` + testUUID + `","initialBalance":{"amount":"1.00","currency":"brl"}}`,
		"sem saldo":          `{"playerId":"` + testUUID + `"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			svc := &fakeWallets{}
			rec := do(newTestHandler(svc, nil), http.MethodPost, "/wallets", body)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "INVALID_REQUEST" {
				t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
			}
			if svc.calls != 0 {
				t.Fatal("caso de uso não deveria ser chamado")
			}
		})
	}
}

func TestOpenWalletConflict(t *testing.T) {
	rec := do(newTestHandler(&fakeWallets{err: app.ErrConflict}, nil), http.MethodPost, "/wallets", openBody)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "CONFLICT" {
		t.Fatalf("status %d, corpo %s", rec.Code, rec.Body)
	}
}
