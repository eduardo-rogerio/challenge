package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"desafio/internal/app"
)

func TestIsUUID(t *testing.T) {
	for s, want := range map[string]bool{
		"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1": true,
		"":                                     false,
		"not-a-uuid":                           false,
		"0192f28f5dc07d58bdb2814ad6a0f4a1":     false,
	} {
		if got := isUUID(s); got != want {
			t.Errorf("isUUID(%q) = %v", s, got)
		}
	}
}

func TestValidMoney(t *testing.T) {
	tests := []struct {
		amount, currency string
		ok               bool
	}{
		{"25.00", "BRL", true},
		{"0.00", "BRL", true},
		{"10", "BRL", false},
		{"1e3", "BRL", false},
		{"-1.00", "BRL", false},
		{"1.234", "BRL", false},
		{"1.5", "BRL", false},
		{"", "BRL", false},
		{"NaN", "BRL", false},
		{"25.00", "brl", false},
		{"25.00", "BR", false},
		{"25.00", "", false},
	}
	for _, tt := range tests {
		if got := validMoney(app.Money{Amount: tt.amount, Currency: tt.currency}); got != tt.ok {
			t.Errorf("validMoney(%q, %q) = %v", tt.amount, tt.currency, got)
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	type dst struct {
		A string `json:"a"`
	}
	tests := map[string]bool{
		`{"a":"x"}`:           true,
		`{"a":"x","b":1}`:     false, // campo desconhecido
		`{"a":1}`:             false, // tipo errado
		`{"a":"x"} {"a":"y"}`: false, // conteúdo extra
		`nope`:                false,
		``:                    false,
	}
	for body, ok := range tests {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var d dst
		if err := decodeJSON(httptest.NewRecorder(), r, &d); (err == nil) != ok {
			t.Errorf("decodeJSON(%q) err=%v, ok esperado=%v", body, err, ok)
		}
	}
}

func TestValidIdempotencyKey(t *testing.T) {
	tests := map[string]bool{
		"provider-a:transaction-123": true,
		"a":                          true,
		strings.Repeat("k", 255):     true,
		"":                           false,
		strings.Repeat("k", 256):     false,
		"com espaço":                 false,
		"tab\there":                  false,
		"acentuação":                 false,
		"nova\nlinha":                false,
	}
	for k, want := range tests {
		if got := validIdempotencyKey(k); got != want {
			t.Errorf("validIdempotencyKey(%q) = %v", k, got)
		}
	}
}
