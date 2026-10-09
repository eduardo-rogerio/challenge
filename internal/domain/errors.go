package domain

import "errors"

// Erros de domínio; use errors.Is para classificá-los. Rejeições de negócio não são erros:
// elas levam a transação ao estado REJECTED (ver Apply).
var (
	ErrInvalidMoney      = errors.New("valor monetário inválido")
	ErrInvalidCurrency   = errors.New("moeda inválida")
	ErrCurrencyMismatch  = errors.New("moedas incompatíveis")
	ErrOverflow          = errors.New("valor fora do limite representável")
	ErrInvalidID         = errors.New("identificador inválido")
	ErrInvalidState      = errors.New("estado ou dados inválidos")
	ErrInsufficientFunds = errors.New("saldo insuficiente")
	ErrInvalidTransition = errors.New("transição de estado inválida")
	ErrInconsistentEntry = errors.New("lançamento de ledger inconsistente")
)
