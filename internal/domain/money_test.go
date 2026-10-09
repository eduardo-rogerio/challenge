package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseMoneyValid(t *testing.T) {
	tests := []struct{ in, want string }{
		{"25.00", "25.00"},
		{"0.00", "0.00"},
		{"0.05", "0.05"},
		{"1000.99", "1000.99"},
		{"007.10", "7.10"}, // zeros à esquerda são normalizados
		{"92233720368547758.07", "92233720368547758.07"},
	}
	for _, tt := range tests {
		m, err := ParseMoney(tt.in, "BRL")
		if err != nil {
			t.Errorf("ParseMoney(%q): %v", tt.in, err)
			continue
		}
		if m.Amount() != tt.want || m.Currency() != "BRL" {
			t.Errorf("ParseMoney(%q) = %s", tt.in, m)
		}
	}
}

func TestParseMoneyInvalid(t *testing.T) {
	for _, in := range []string{"", "10", "1.5", "1.234", "-1.00", "+1.00", "1e3", "1.00e2", "NaN", "Infinity", " 1.00", "1.00 ", "1,00", ".50", "1."} {
		if _, err := ParseMoney(in, "BRL"); !errors.Is(err, ErrInvalidMoney) {
			t.Errorf("ParseMoney(%q): erro %v, esperado ErrInvalidMoney", in, err)
		}
	}
}

func TestParseMoneyInvalidCurrency(t *testing.T) {
	for _, c := range []string{"", "brl", "BR", "BRLL", "B1L"} {
		if _, err := ParseMoney("1.00", c); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("moeda %q: erro %v, esperado ErrInvalidCurrency", c, err)
		}
		if _, err := ZeroMoney(c); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("ZeroMoney(%q): erro %v", c, err)
		}
	}
}

func TestParseMoneyOverflow(t *testing.T) {
	for _, in := range []string{"92233720368547758.08", "99999999999999999999.99"} {
		if _, err := ParseMoney(in, "BRL"); !errors.Is(err, ErrOverflow) {
			t.Errorf("ParseMoney(%q): erro %v, esperado ErrOverflow", in, err)
		}
	}
}

func TestZeroMoneyAndPredicates(t *testing.T) {
	z, err := ZeroMoney("BRL")
	if err != nil {
		t.Fatal(err)
	}
	if !z.IsZero() || z.IsPositive() || z.IsNegative() || z.Amount() != "0.00" {
		t.Fatalf("zero inesperado: %+v", z)
	}
	if (Money{}).IsZero() {
		t.Error("o valor zero da linguagem não deve ser considerado zero válido")
	}
}

func TestMoneyJSON(t *testing.T) {
	b, err := json.Marshal(mustMoney(t, "25.00"))
	if err != nil || string(b) != `{"amount":"25.00","currency":"BRL"}` {
		t.Fatalf("json = %s, %v", b, err)
	}
	if _, err := json.Marshal(Money{}); err == nil {
		t.Error("serializar o valor zero da linguagem deveria falhar")
	}
}

func TestMoneyAddSub(t *testing.T) {
	a, b := mustMoney(t, "10.00"), mustMoney(t, "2.50")
	sum, err := a.Add(b)
	if err != nil || sum.Amount() != "12.50" {
		t.Errorf("soma = %v, %v", sum, err)
	}
	diff, err := a.Sub(b)
	if err != nil || diff.Amount() != "7.50" {
		t.Errorf("subtração = %v, %v", diff, err)
	}
}

func TestMoneyNegativeDifference(t *testing.T) {
	d, err := mustMoney(t, "2.00").Sub(mustMoney(t, "5.00"))
	if err != nil || d.Amount() != "-3.00" || !d.IsNegative() {
		t.Fatalf("diferença = %v, %v", d, err)
	}
	n, err := d.Neg()
	if err != nil || n.Amount() != "3.00" {
		t.Fatalf("negação = %v, %v", n, err)
	}
}

func TestMoneyOverflow(t *testing.T) {
	max := mustMoney(t, "92233720368547758.07")
	one := mustMoney(t, "0.01")
	if _, err := max.Add(one); !errors.Is(err, ErrOverflow) {
		t.Errorf("max + 0.01: %v", err)
	}
	zero, _ := ZeroMoney("BRL")
	negMax, _ := zero.Sub(max) // -max é representável
	if _, err := negMax.Sub(one); err != nil {
		t.Errorf("-max - 0.01 = MinInt64 é representável: %v", err)
	}
	min := Money{minor: math.MinInt64, currency: "BRL"}
	if _, err := min.Sub(one); !errors.Is(err, ErrOverflow) {
		t.Errorf("min - 0.01: %v", err)
	}
	if _, err := min.Add(min); !errors.Is(err, ErrOverflow) {
		t.Errorf("min + min: %v", err)
	}
	if _, err := zero.Sub(min); !errors.Is(err, ErrOverflow) {
		t.Errorf("0 - min: %v", err)
	}
	if _, err := min.Neg(); !errors.Is(err, ErrOverflow) {
		t.Errorf("-min: %v", err)
	}
	if min.Amount() != "-92233720368547758.08" {
		t.Errorf("formatação de MinInt64 = %s", min.Amount())
	}
}

func TestMoneyCurrencyMismatch(t *testing.T) {
	brl := mustMoney(t, "1.00")
	usd, _ := ParseMoney("1.00", "USD")
	if _, err := brl.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Add: %v", err)
	}
	if _, err := brl.Sub(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Sub: %v", err)
	}
	if _, err := brl.Cmp(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Cmp: %v", err)
	}
	if brl.Equal(usd) {
		t.Error("Equal entre moedas diferentes deve ser falso")
	}
}

func TestMoneyUninitializedRejected(t *testing.T) {
	var zero Money
	ok := mustMoney(t, "1.00")
	if _, err := zero.Add(ok); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Add: %v", err)
	}
	if _, err := ok.Sub(zero); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Sub: %v", err)
	}
	if _, err := zero.Neg(); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Neg: %v", err)
	}
	if _, err := zero.Cmp(ok); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Cmp: %v", err)
	}
}

func TestMoneyCmp(t *testing.T) {
	a, b := mustMoney(t, "1.00"), mustMoney(t, "2.00")
	for _, tt := range []struct {
		x, y Money
		want int
	}{{a, b, -1}, {b, a, 1}, {a, a, 0}} {
		if got, err := tt.x.Cmp(tt.y); err != nil || got != tt.want {
			t.Errorf("Cmp(%s,%s) = %d, %v", tt.x, tt.y, got, err)
		}
	}
	if !a.Equal(mustMoney(t, "1.00")) {
		t.Error("valores iguais devem ser Equal")
	}
}

func TestMinorRoundTrip(t *testing.T) {
	m := mustMoney(t, "25.07")
	if m.Minor() != 2507 {
		t.Fatalf("Minor = %d", m.Minor())
	}
	back, err := NewMoneyFromMinor(m.Minor(), m.Currency())
	if err != nil || !back.Equal(m) {
		t.Fatalf("ida e volta: %v, %v", back, err)
	}
	neg, err := NewMoneyFromMinor(-300, "BRL")
	if err != nil || neg.Amount() != "-3.00" {
		t.Fatalf("negativo: %v, %v", neg, err)
	}
	if _, err := NewMoneyFromMinor(1, "brl"); !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("moeda inválida: %v", err)
	}
}
