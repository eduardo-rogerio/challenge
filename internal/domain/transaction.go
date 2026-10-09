package domain

import (
	"fmt"
	"slices"
	"time"
)

type Kind string

const (
	KindOpening  Kind = "OPENING" // reservado à abertura interna de carteira
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

func (k Kind) external() bool {
	return slices.Contains([]Kind{KindBet, KindWin, KindLoss, KindRefund, KindRollback}, k)
}

func (k Kind) reversal() bool { return k == KindRefund || k == KindRollback }

type Status string

const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

// transitions lista as únicas mudanças de estado permitidas; PROCESSED, REJECTED e FAILED são terminais.
var transitions = map[Status][]Status{
	StatusPending:          {StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed},
	StatusPendingReference: {StatusProcessed, StatusRejected, StatusFailed},
}

func (s Status) terminal() bool { return len(transitions[s]) == 0 && s.known() }

func (s Status) known() bool {
	return slices.Contains([]Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}, s)
}

// FailureCode é o código estável de uma rejeição ou falha.
type FailureCode string

// WagerTransaction registra uma operação de uma carteira (externa ou a abertura interna).
type WagerTransaction struct {
	id                  ID
	providerID          string
	externalID          string
	idempotencyKey      string
	requestHash         string
	walletID            ID
	playerID            ID
	roundID             string
	gameID              string
	kind                Kind
	money               Money
	referenceExternalID string
	resolvedReferenceID ID
	status              Status
	failureCode         FailureCode
	resultBalance       Money // saldo observado ao processar; zero da linguagem enquanto não processada
	createdAt           time.Time
	updatedAt           time.Time
}

// ExternalParams são os dados de uma operação recebida de um provedor.
type ExternalParams struct {
	ID                  ID
	ProviderID          string
	ExternalID          string
	IdempotencyKey      string
	RequestHash         string
	WalletID            ID
	PlayerID            ID
	RoundID             string
	GameID              string
	Kind                Kind
	Money               Money
	ReferenceExternalID string
	Now                 time.Time
}

// NewExternalTransaction cria uma operação externa em PENDING.
func NewExternalTransaction(p ExternalParams) (*WagerTransaction, error) {
	t := &WagerTransaction{
		id: p.ID, providerID: p.ProviderID, externalID: p.ExternalID,
		idempotencyKey: p.IdempotencyKey, requestHash: p.RequestHash,
		walletID: p.WalletID, playerID: p.PlayerID, roundID: p.RoundID, gameID: p.GameID,
		kind: p.Kind, money: p.Money, referenceExternalID: p.ReferenceExternalID,
		status: StatusPending, createdAt: p.Now, updatedAt: p.Now,
	}
	if err := t.validateExternal(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *WagerTransaction) validateExternal() error {
	switch {
	case !t.id.valid() || !t.walletID.valid() || !t.playerID.valid():
		return fmt.Errorf("%w: id, carteira e jogador devem ser UUIDs", ErrInvalidID)
	case t.providerID == "" || t.externalID == "" || t.idempotencyKey == "" || t.requestHash == "":
		return fmt.Errorf("%w: provedor, id externo, chave e hash são obrigatórios", ErrInvalidState)
	case t.roundID == "" || t.gameID == "":
		return fmt.Errorf("%w: rodada e jogo são obrigatórios", ErrInvalidState)
	case !t.kind.external():
		return fmt.Errorf("%w: tipo %q não é uma operação externa", ErrInvalidState, t.kind)
	case !t.money.IsValid():
		return fmt.Errorf("%w: valor não inicializado", ErrInvalidMoney)
	case t.kind == KindLoss && !t.money.IsZero():
		return fmt.Errorf("%w: LOSS exige valor zero", ErrInvalidMoney)
	case t.kind != KindLoss && !t.money.IsPositive():
		return fmt.Errorf("%w: %s exige valor positivo", ErrInvalidMoney, t.kind)
	case t.kind.reversal() && t.referenceExternalID == "":
		return fmt.Errorf("%w: %s exige referência externa", ErrInvalidState, t.kind)
	case t.createdAt.IsZero() || t.updatedAt.IsZero() || t.updatedAt.Before(t.createdAt):
		return fmt.Errorf("%w: timestamps inválidos", ErrInvalidState)
	}
	return nil
}

// TransactionSnapshot é o estado persistido de uma transação, usado só para reidratar.
type TransactionSnapshot struct {
	ID                  ID
	ProviderID          string
	ExternalID          string
	IdempotencyKey      string
	RequestHash         string
	WalletID            ID
	PlayerID            ID
	RoundID             string
	GameID              string
	Kind                Kind
	Money               Money
	ReferenceExternalID string
	ResolvedReferenceID ID
	Status              Status
	FailureCode         FailureCode
	ResultBalance       Money
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RehydrateTransaction reconstrói uma transação persistida validando a coerência entre estado e campos,
// sem reaplicar transições nem produzir efeitos.
func RehydrateTransaction(s TransactionSnapshot) (*WagerTransaction, error) {
	t := &WagerTransaction{
		id: s.ID, providerID: s.ProviderID, externalID: s.ExternalID,
		idempotencyKey: s.IdempotencyKey, requestHash: s.RequestHash,
		walletID: s.WalletID, playerID: s.PlayerID, roundID: s.RoundID, gameID: s.GameID,
		kind: s.Kind, money: s.Money, referenceExternalID: s.ReferenceExternalID,
		resolvedReferenceID: s.ResolvedReferenceID, status: s.Status, failureCode: s.FailureCode,
		resultBalance: s.ResultBalance, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt,
	}
	if t.kind == KindOpening {
		if err := t.validateOpening(); err != nil {
			return nil, err
		}
	} else if err := t.validateExternal(); err != nil {
		return nil, err
	}
	if err := t.validateStatusFields(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *WagerTransaction) validateOpening() error {
	switch {
	case !t.id.valid() || !t.walletID.valid() || !t.playerID.valid():
		return fmt.Errorf("%w: id, carteira e jogador devem ser UUIDs", ErrInvalidID)
	case t.providerID != "" || t.externalID != "" || t.idempotencyKey != "" || t.requestHash != "" ||
		t.roundID != "" || t.gameID != "" || t.referenceExternalID != "":
		return fmt.Errorf("%w: OPENING não tem metadados externos", ErrInvalidState)
	case !t.money.IsPositive():
		return fmt.Errorf("%w: OPENING exige valor positivo", ErrInvalidMoney)
	case t.status != StatusProcessed:
		return fmt.Errorf("%w: OPENING nasce PROCESSED", ErrInvalidState)
	case t.createdAt.IsZero() || t.updatedAt.Before(t.createdAt):
		return fmt.Errorf("%w: timestamps inválidos", ErrInvalidState)
	}
	return nil
}

func (t *WagerTransaction) validateStatusFields() error {
	if !t.status.known() {
		return fmt.Errorf("%w: estado %q", ErrInvalidState, t.status)
	}
	switch t.status {
	case StatusRejected, StatusFailed:
		if t.failureCode == "" {
			return fmt.Errorf("%w: %s exige failureCode", ErrInvalidState, t.status)
		}
		if t.resultBalance.IsValid() {
			return fmt.Errorf("%w: %s não tem saldo observado", ErrInvalidState, t.status)
		}
	case StatusProcessed:
		if t.failureCode != "" {
			return fmt.Errorf("%w: PROCESSED não tem failureCode", ErrInvalidState)
		}
		if !t.resultBalance.IsValid() {
			return fmt.Errorf("%w: PROCESSED exige o saldo observado", ErrInvalidState)
		}
	default:
		if t.failureCode != "" || t.resultBalance.IsValid() {
			return fmt.Errorf("%w: %s não tem failureCode nem saldo observado", ErrInvalidState, t.status)
		}
	}
	return nil
}

func (t *WagerTransaction) ID() ID                      { return t.id }
func (t *WagerTransaction) ProviderID() string          { return t.providerID }
func (t *WagerTransaction) ExternalID() string          { return t.externalID }
func (t *WagerTransaction) IdempotencyKey() string      { return t.idempotencyKey }
func (t *WagerTransaction) RequestHash() string         { return t.requestHash }
func (t *WagerTransaction) WalletID() ID                { return t.walletID }
func (t *WagerTransaction) PlayerID() ID                { return t.playerID }
func (t *WagerTransaction) RoundID() string             { return t.roundID }
func (t *WagerTransaction) GameID() string              { return t.gameID }
func (t *WagerTransaction) Kind() Kind                  { return t.kind }
func (t *WagerTransaction) Money() Money                { return t.money }
func (t *WagerTransaction) ReferenceExternalID() string { return t.referenceExternalID }
func (t *WagerTransaction) ResolvedReferenceID() ID     { return t.resolvedReferenceID }
func (t *WagerTransaction) Status() Status              { return t.status }
func (t *WagerTransaction) FailureCode() FailureCode    { return t.failureCode }
func (t *WagerTransaction) CreatedAt() time.Time        { return t.createdAt }
func (t *WagerTransaction) UpdatedAt() time.Time        { return t.updatedAt }

// ResultBalance devolve o saldo da carteira observado ao processar a transação.
func (t *WagerTransaction) ResultBalance() (Money, bool) {
	return t.resultBalance, t.resultBalance.IsValid()
}

func (t *WagerTransaction) transition(to Status, now time.Time) error {
	if !slices.Contains(transitions[t.status], to) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.status, to)
	}
	if now.IsZero() || now.Before(t.updatedAt) {
		return fmt.Errorf("%w: instante inválido", ErrInvalidState)
	}
	t.status = to
	t.updatedAt = now
	return nil
}

// MarkPendingReference registra a espera por uma referência ainda indisponível.
func (t *WagerTransaction) MarkPendingReference(now time.Time) error {
	return t.transition(StatusPendingReference, now)
}

// MarkProcessed conclui a transação com o saldo da carteira observado.
func (t *WagerTransaction) MarkProcessed(balance Money, now time.Time) error {
	if !balance.IsValid() {
		return fmt.Errorf("%w: saldo observado não inicializado", ErrInvalidMoney)
	}
	if err := t.transition(StatusProcessed, now); err != nil {
		return err
	}
	t.resultBalance = balance
	return nil
}

// Reject conclui a transação como recusada por regra de negócio.
func (t *WagerTransaction) Reject(code FailureCode, now time.Time) error {
	return t.finishWithCode(StatusRejected, code, now)
}

// Fail conclui a transação como falha permanente de infraestrutura.
func (t *WagerTransaction) Fail(code FailureCode, now time.Time) error {
	return t.finishWithCode(StatusFailed, code, now)
}

func (t *WagerTransaction) finishWithCode(to Status, code FailureCode, now time.Time) error {
	if code == "" {
		return fmt.Errorf("%w: failureCode é obrigatório", ErrInvalidState)
	}
	if err := t.transition(to, now); err != nil {
		return err
	}
	t.failureCode = code
	return nil
}
