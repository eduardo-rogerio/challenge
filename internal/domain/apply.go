package domain

import (
	"errors"
	"fmt"
	"time"
)

// Códigos de falha estáveis. Corrigíveis pelo cliente: WALLET_MISMATCH, REFERENCE_MISMATCH,
// REFERENCE_AMOUNT_MISMATCH e INVALID_REFERENCE_KIND; os demais são resultados definitivos.
// REFERENCE_NOT_FOUND é atribuído pelo chamador (tx.Reject) ao esgotar tentativas ou TTL.
const (
	FailureInsufficientFunds         FailureCode = "INSUFFICIENT_FUNDS"
	FailureReversalInsufficientFunds FailureCode = "REVERSAL_INSUFFICIENT_FUNDS"
	FailureWalletMismatch            FailureCode = "WALLET_MISMATCH"
	FailureReferenceNotFound         FailureCode = "REFERENCE_NOT_FOUND"
	FailureReferenceNotProcessed     FailureCode = "REFERENCE_NOT_PROCESSED"
	FailureReferenceMismatch         FailureCode = "REFERENCE_MISMATCH"
	FailureReferenceAmountMismatch   FailureCode = "REFERENCE_AMOUNT_MISMATCH"
	FailureInvalidReferenceKind      FailureCode = "INVALID_REFERENCE_KIND"
	FailureReferenceAlreadyReversed  FailureCode = "REFERENCE_ALREADY_REVERSED"
)

// Reference descreve a transação referenciada por uma reversão (ou por uma WIN), já resolvida
// pelo chamador: Tx é nulo quando ela ainda não existe e Reversed indica que ela já recebeu uma
// reversão bem-sucedida (de qualquer tipo).
type Reference struct {
	Tx       *WagerTransaction
	Reversed bool
}

// Apply aplica uma operação externa pendente à carteira.
//
// Rejeições de negócio não são erros: a transação vai a REJECTED com um failureCode e Apply devolve
// (nil, nil). Quando a operação movimenta saldo, a carteira é alterada, a transação vai a PROCESSED
// e o lançamento correspondente é devolvido. Uma reversão sem referência utilizável vai a
// PENDING_REFERENCE. O erro fica reservado a mau uso (argumentos nulos, transação já terminal) ou
// falha inesperada, e nesses casos nem a carteira nem a transação são alteradas.
//
// Política de reversões: cada transação referenciada recebe no máximo uma reversão bem-sucedida,
// de qualquer tipo; assim o mesmo débito nunca é devolvido duas vezes.
func Apply(w *Wallet, tx *WagerTransaction, ref Reference, entryID ID, now time.Time) (*WalletLedgerEntry, error) {
	if w == nil || tx == nil {
		return nil, fmt.Errorf("%w: carteira e transação são obrigatórias", ErrInvalidState)
	}
	if tx.kind == KindOpening {
		return nil, fmt.Errorf("%w: OPENING não é aplicável", ErrInvalidState)
	}
	if now.IsZero() || now.Before(tx.updatedAt) || now.Before(w.updatedAt) {
		return nil, fmt.Errorf("%w: instante anterior ao último estado", ErrInvalidState)
	}
	if tx.status != StatusPending && tx.status != StatusPendingReference {
		return nil, fmt.Errorf("%w: transação %s não pode ser aplicada", ErrInvalidTransition, tx.status)
	}
	if tx.walletID != w.id || tx.playerID != w.playerID || tx.money.Currency() != w.Currency() {
		return nil, tx.Reject(FailureWalletMismatch, now)
	}

	switch tx.kind {
	case KindBet:
		return move(w, tx, DirectionDebit, entryID, now, FailureInsufficientFunds)
	case KindWin:
		if code := checkWinReference(tx, ref); code != "" {
			return nil, tx.Reject(code, now)
		}
		return move(w, tx, DirectionCredit, entryID, now, "")
	case KindLoss:
		return nil, tx.MarkProcessed(w.balance, now)
	default: // KindRefund, KindRollback
		return applyReversal(w, tx, ref, entryID, now)
	}
}

// checkWinReference valida a referência opcional de uma WIN (uma BET da mesma rodada) quando ela
// foi informada e já está resolvida; ausente ou ainda pendente não bloqueia o ganho.
func checkWinReference(tx *WagerTransaction, ref Reference) FailureCode {
	if tx.referenceExternalID == "" || ref.Tx == nil {
		return ""
	}
	if !sameContext(tx, ref.Tx) {
		return FailureReferenceMismatch
	}
	if ref.Tx.kind != KindBet {
		return FailureInvalidReferenceKind
	}
	return ""
}

func applyReversal(w *Wallet, tx *WagerTransaction, ref Reference, entryID ID, now time.Time) (*WalletLedgerEntry, error) {
	r := ref.Tx
	if r == nil {
		return nil, waitReference(tx, now)
	}
	switch r.status {
	case StatusPending, StatusPendingReference:
		return nil, waitReference(tx, now)
	case StatusRejected, StatusFailed:
		return nil, tx.Reject(FailureReferenceNotProcessed, now)
	}

	if !sameContext(tx, r) {
		return nil, tx.Reject(FailureReferenceMismatch, now)
	}
	if code := reversalKindCheck(tx.kind, r.kind); code != "" {
		return nil, tx.Reject(code, now)
	}
	if !r.money.Equal(tx.money) {
		return nil, tx.Reject(FailureReferenceAmountMismatch, now)
	}
	if ref.Reversed {
		return nil, tx.Reject(FailureReferenceAlreadyReversed, now)
	}

	tx.resolvedReferenceID = r.id
	dir := DirectionCredit // REFUND e ROLLBACK de BET devolvem o débito
	if tx.kind == KindRollback && r.kind != KindBet {
		dir = DirectionDebit // ROLLBACK de WIN ou REFUND desfaz um crédito
	}
	return move(w, tx, dir, entryID, now, FailureReversalInsufficientFunds)
}

// reversalKindCheck: REFUND só devolve uma BET; ROLLBACK desfaz BET, WIN ou REFUND.
func reversalKindCheck(reversal, referenced Kind) FailureCode {
	switch {
	case reversal == KindRefund && referenced == KindBet,
		reversal == KindRollback && (referenced == KindBet || referenced == KindWin || referenced == KindRefund):
		return ""
	}
	return FailureInvalidReferenceKind
}

// sameContext compara provedor, jogador, carteira, moeda e rodada.
func sameContext(a, b *WagerTransaction) bool {
	return a.providerID == b.providerID && a.playerID == b.playerID && a.walletID == b.walletID &&
		a.money.Currency() == b.money.Currency() && a.roundID == b.roundID
}

// waitReference deixa a reversão em PENDING_REFERENCE; se já estiver, nada muda.
func waitReference(tx *WagerTransaction, now time.Time) error {
	if tx.status == StatusPendingReference {
		return nil
	}
	return tx.MarkPendingReference(now)
}

// move movimenta a carteira e conclui a transação. Saldo insuficiente rejeita a transação com o
// código informado; qualquer outro erro é devolvido sem alterar nada.
func move(w *Wallet, tx *WagerTransaction, dir Direction, entryID ID, now time.Time, insufficient FailureCode) (*WalletLedgerEntry, error) {
	var entry *WalletLedgerEntry
	var err error
	if dir == DirectionCredit {
		entry, err = w.Credit(entryID, tx.id, tx.money, now)
	} else {
		entry, err = w.Debit(entryID, tx.id, tx.money, now)
	}
	if errors.Is(err, ErrInsufficientFunds) {
		return nil, tx.Reject(insufficient, now)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.MarkProcessed(w.balance, now); err != nil {
		return nil, err
	}
	return entry, nil
}
