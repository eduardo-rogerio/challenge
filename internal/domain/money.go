package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Money é um valor monetário imutável em unidades mínimas (centavos, escala fixa de 2) de uma moeda.
// O intervalo vai de -92233720368547758.08 a 92233720368547758.07 (limites de int64).
// O valor zero da linguagem (sem moeda) é inválido em todas as operações.
type Money struct {
	minor    int64
	currency string
}

var (
	amountRE   = regexp.MustCompile(`^[0-9]+\.[0-9]{2}$`)
	currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)
)

// ParseMoney cria um valor externo: string decimal com exatamente duas casas, sem sinal,
// sem notação científica e sem arredondamento. A moeda é um código de três letras maiúsculas.
func ParseMoney(amount, currency string) (Money, error) {
	if !currencyRE.MatchString(currency) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	if !amountRE.MatchString(amount) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidMoney, amount)
	}
	digits := amount[:len(amount)-3] + amount[len(amount)-2:]
	minor, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return Money{}, fmt.Errorf("%w: %q", ErrOverflow, amount)
		}
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidMoney, amount)
	}
	return Money{minor: minor, currency: currency}, nil
}

// NewMoneyFromMinor reconstrói um valor de unidades mínimas já validadas (por exemplo, lidas do banco).
func NewMoneyFromMinor(minor int64, currency string) (Money, error) {
	if !currencyRE.MatchString(currency) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{minor: minor, currency: currency}, nil
}

// ZeroMoney devolve o zero de uma moeda.
func ZeroMoney(currency string) (Money, error) {
	if !currencyRE.MatchString(currency) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{currency: currency}, nil
}

func (m Money) Currency() string { return m.currency }

// Minor devolve o valor em unidades mínimas (centavos).
func (m Money) Minor() int64 { return m.minor }

func (m Money) IsValid() bool    { return m.currency != "" }
func (m Money) IsZero() bool     { return m.IsValid() && m.minor == 0 }
func (m Money) IsPositive() bool { return m.IsValid() && m.minor > 0 }
func (m Money) IsNegative() bool { return m.IsValid() && m.minor < 0 }

// Amount devolve a string decimal com duas casas, com "-" quando negativo.
func (m Money) Amount() string {
	u := uint64(m.minor)
	sign := ""
	if m.minor < 0 {
		sign = "-"
		u = uint64(-(m.minor + 1)) + 1 // seguro para math.MinInt64
	}
	return fmt.Sprintf("%s%d.%02d", sign, u/100, u%100)
}

func (m Money) String() string { return m.Amount() + " " + m.currency }

func (m Money) MarshalJSON() ([]byte, error) {
	if !m.IsValid() {
		return nil, ErrInvalidMoney
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{m.Amount(), m.currency})
}

// compatible valida que ambos os valores foram inicializados e têm a mesma moeda.
func compatible(a, b Money) error {
	if !a.IsValid() || !b.IsValid() {
		return ErrInvalidMoney
	}
	if a.currency != b.currency {
		return fmt.Errorf("%w: %s e %s", ErrCurrencyMismatch, a.currency, b.currency)
	}
	return nil
}

func (m Money) Add(o Money) (Money, error) {
	if err := compatible(m, o); err != nil {
		return Money{}, err
	}
	s := m.minor + o.minor
	if (m.minor > 0 && o.minor > 0 && s < 0) || (m.minor < 0 && o.minor < 0 && s >= 0) {
		return Money{}, ErrOverflow
	}
	return Money{minor: s, currency: m.currency}, nil
}

func (m Money) Sub(o Money) (Money, error) {
	if err := compatible(m, o); err != nil {
		return Money{}, err
	}
	d := m.minor - o.minor
	if (m.minor >= 0 && o.minor < 0 && d < 0) || (m.minor < 0 && o.minor > 0 && d >= 0) {
		return Money{}, ErrOverflow
	}
	return Money{minor: d, currency: m.currency}, nil
}

func (m Money) Neg() (Money, error) {
	if !m.IsValid() {
		return Money{}, ErrInvalidMoney
	}
	if m.minor == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{minor: -m.minor, currency: m.currency}, nil
}

// Cmp devolve -1, 0 ou 1; falha se as moedas forem incompatíveis.
func (m Money) Cmp(o Money) (int, error) {
	if err := compatible(m, o); err != nil {
		return 0, err
	}
	switch {
	case m.minor < o.minor:
		return -1, nil
	case m.minor > o.minor:
		return 1, nil
	}
	return 0, nil
}

// Equal é verdadeiro apenas para valores válidos, de mesma moeda e mesmo montante.
func (m Money) Equal(o Money) bool {
	c, err := m.Cmp(o)
	return err == nil && c == 0
}
