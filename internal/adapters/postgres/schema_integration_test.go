//go:build integration

package postgres_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SQLSTATE usados nas verificações.
const (
	sqlCheckViolation    = "23514"
	sqlUniqueViolation   = "23505"
	sqlRestrictViolation = "23001"
)

// Os testes de banco usam ids aleatórios e nunca truncam tabelas, para que possam coexistir
// com dados de outros testes e execuções.
func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func wantPgCode(t *testing.T, err error, code, what string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s: esperava erro do PostgreSQL %s, obtido %v", what, code, err)
		return
	}
	if pgErr.Code != code {
		t.Errorf("%s: SQLSTATE %s (%s), esperado %s", what, pgErr.Code, pgErr.Message, code)
	}
}

func execOK(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func execErr(pool *pgxpool.Pool, sql string, args ...any) error {
	_, err := pool.Exec(context.Background(), sql, args...)
	return err
}

func insertWallet(t *testing.T, pool *pgxpool.Pool, balance int64) (walletID, playerID string) {
	t.Helper()
	walletID, playerID = uuid4(), uuid4()
	execOK(t, pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'BRL', $3, 1, now(), now())`, walletID, playerID, balance)
	return walletID, playerID
}

// insertExternalTx insere uma transação externa PROCESSED e devolve seu id.
func insertExternalTx(t *testing.T, pool *pgxpool.Pool, walletID, playerID, kind string, amount int64, extID string) string {
	t.Helper()
	id := uuid4()
	execOK(t, pool, externalTxSQL, id, walletID, playerID, kind, amount, "PROCESSED", nil, 100, "prov", extID, "k-"+extID, "h", nil, nil)
	return id
}

const externalTxSQL = `
INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, failure_code, result_balance,
    provider_id, external_transaction_id, idempotency_key, request_hash, round_id, game_id,
    reference_external_transaction_id, resolved_reference_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 'BRL', $6, $7, $8, $9, $10, $11, $12, 'round', 'game', $13, $14, now(), now())`

func insertLedger(pool *pgxpool.Pool, walletID, txID, dir string, amount, before, after int64) error {
	return execErr(pool, `INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction, amount, balance_before, balance_after, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())`, uuid4(), walletID, txID, dir, amount, before, after)
}

func TestSchemaWalletConstraints(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)

	wantPgCode(t, execErr(pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'BRL', -1, 1, now(), now())`, uuid4(), uuid4()), sqlCheckViolation, "saldo negativo na inserção")
	wantPgCode(t, execErr(pool, `UPDATE wallets SET balance = -1 WHERE id = $1`, walletID), sqlCheckViolation, "saldo negativo na atualização")
	wantPgCode(t, execErr(pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'BRL', 0, 0, now(), now())`, uuid4(), uuid4()), sqlCheckViolation, "versão 0")
	wantPgCode(t, execErr(pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'brl', 0, 1, now(), now())`, uuid4(), uuid4()), sqlCheckViolation, "moeda minúscula")
	wantPgCode(t, execErr(pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'BRL', 0, 1, now(), now())`, uuid4(), playerID), sqlUniqueViolation, "segunda carteira do jogador/moeda")

	var balance int64
	if err := pool.QueryRow(context.Background(), `SELECT balance FROM wallets WHERE id = $1`, walletID).Scan(&balance); err != nil || balance != 10000 {
		t.Errorf("saldo preservado = %d, %v", balance, err)
	}
	// outra moeda para o mesmo jogador é permitida
	execOK(t, pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'USD', 0, 1, now(), now())`, uuid4(), playerID)
}

func TestSchemaLedgerConstraints(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	txID := insertExternalTx(t, pool, walletID, playerID, "BET", 2500, "e-"+uuid4())

	wantPgCode(t, insertLedger(pool, walletID, txID, "DEBIT", 2500, 10000, 8000), sqlCheckViolation, "aritmética do débito")
	wantPgCode(t, insertLedger(pool, walletID, txID, "CREDIT", 2500, 10000, 7500), sqlCheckViolation, "aritmética do crédito")
	wantPgCode(t, insertLedger(pool, walletID, txID, "DEBIT", 0, 10000, 10000), sqlCheckViolation, "valor zero")
	wantPgCode(t, insertLedger(pool, walletID, txID, "DEBIT", 20000, 10000, -10000), sqlCheckViolation, "saldo posterior negativo")
	wantPgCode(t, insertLedger(pool, walletID, txID, "SIDEWAYS", 2500, 10000, 7500), sqlCheckViolation, "direção inválida")

	if err := insertLedger(pool, walletID, txID, "DEBIT", 2500, 10000, 7500); err != nil {
		t.Fatalf("lançamento válido: %v", err)
	}
	wantPgCode(t, insertLedger(pool, walletID, txID, "DEBIT", 2500, 7500, 5000), sqlUniqueViolation, "lançamento duplicado (carteira, transação)")
}

func TestSchemaLedgerIsAppendOnly(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	txID := insertExternalTx(t, pool, walletID, playerID, "BET", 2500, "e-"+uuid4())
	if err := insertLedger(pool, walletID, txID, "DEBIT", 2500, 10000, 7500); err != nil {
		t.Fatal(err)
	}

	wantPgCode(t, execErr(pool, `UPDATE wallet_ledger_entries SET amount = 1 WHERE wallet_id = $1`, walletID), sqlRestrictViolation, "UPDATE do ledger")
	wantPgCode(t, execErr(pool, `DELETE FROM wallet_ledger_entries WHERE wallet_id = $1`, walletID), sqlRestrictViolation, "DELETE do ledger")
	wantPgCode(t, execErr(pool, `TRUNCATE wallet_ledger_entries`), sqlRestrictViolation, "TRUNCATE do ledger")

	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id = $1 AND amount = 2500`, walletID).Scan(&n); err != nil || n != 1 {
		t.Errorf("o lançamento deveria estar intacto: %d, %v", n, err)
	}
}

func TestSchemaTransactionUniqueness(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	ext := "e-" + uuid4()
	insertExternalTx(t, pool, walletID, playerID, "BET", 2500, ext)

	// mesma operação externa (provedor + id externo), chave diferente
	wantPgCode(t, execErr(pool, externalTxSQL, uuid4(), walletID, playerID, "BET", 2500, "PROCESSED", nil, 100, "prov", ext, "outra-chave", "h", nil, nil),
		sqlUniqueViolation, "operação externa duplicada")
	// outro provedor com o mesmo id externo é outra operação
	execOK(t, pool, externalTxSQL, uuid4(), walletID, playerID, "BET", 2500, "PROCESSED", nil, 100, "outro-prov", ext, "k2", "h", nil, nil)

	openingSQL := `INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, result_balance, created_at, updated_at)
		VALUES ($1, $2, $3, 'OPENING', 10000, 'BRL', 'PROCESSED', 10000, now(), now())`
	execOK(t, pool, openingSQL, uuid4(), walletID, playerID)
	wantPgCode(t, execErr(pool, openingSQL, uuid4(), walletID, playerID), sqlUniqueViolation, "segunda OPENING da carteira")
}

func TestSchemaSingleSuccessfulReversalPerReference(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	bet := insertExternalTx(t, pool, walletID, playerID, "BET", 2500, "bet-"+uuid4())

	insertReversal := func(kind, status string, failure any, balance any) error {
		return execErr(pool, externalTxSQL, uuid4(), walletID, playerID, kind, 2500, status, failure, balance,
			"prov", "rev-"+uuid4(), "k-"+uuid4(), "h", "ref-ext", bet)
	}
	if err := insertReversal("REFUND", "PROCESSED", nil, 100); err != nil {
		t.Fatalf("primeira reversão: %v", err)
	}
	wantPgCode(t, insertReversal("ROLLBACK", "PROCESSED", nil, 100), sqlUniqueViolation, "segunda reversão PROCESSED")
	wantPgCode(t, insertReversal("REFUND", "PROCESSED", nil, 100), sqlUniqueViolation, "segundo REFUND PROCESSED")
	// reversões não bem-sucedidas da mesma referência são auditáveis e permitidas
	if err := insertReversal("ROLLBACK", "REJECTED", "REFERENCE_ALREADY_REVERSED", nil); err != nil {
		t.Fatalf("rejeição auditável: %v", err)
	}
}

func TestSchemaTransactionStateCoherence(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	ins := func(kind string, amount int64, status string, failure, balance any, ref any) error {
		return execErr(pool, externalTxSQL, uuid4(), walletID, playerID, kind, amount, status, failure, balance,
			"prov", "e-"+uuid4(), "k-"+uuid4(), "h", ref, nil)
	}
	wantPgCode(t, ins("BET", 100, "REJECTED", nil, nil, nil), sqlCheckViolation, "REJECTED sem failure_code")
	wantPgCode(t, ins("BET", 100, "FAILED", nil, nil, nil), sqlCheckViolation, "FAILED sem failure_code")
	wantPgCode(t, ins("BET", 100, "PROCESSED", "X", 100, nil), sqlCheckViolation, "PROCESSED com failure_code")
	wantPgCode(t, ins("BET", 100, "PROCESSED", nil, nil, nil), sqlCheckViolation, "PROCESSED sem saldo observado")
	wantPgCode(t, ins("BET", 100, "PENDING", nil, 100, nil), sqlCheckViolation, "PENDING com saldo observado")
	wantPgCode(t, ins("BET", 100, "BOGUS", nil, nil, nil), sqlCheckViolation, "estado desconhecido")
	wantPgCode(t, ins("BET", 0, "PENDING", nil, nil, nil), sqlCheckViolation, "BET com valor zero")
	wantPgCode(t, ins("LOSS", 100, "PENDING", nil, nil, nil), sqlCheckViolation, "LOSS com valor")
	wantPgCode(t, ins("REFUND", 100, "PENDING", nil, nil, nil), sqlCheckViolation, "REFUND sem referência")
	if err := ins("LOSS", 0, "PENDING", nil, nil, nil); err != nil {
		t.Errorf("LOSS com zero é válida: %v", err)
	}

	// OPENING não pode ter metadados externos nem estado diferente de PROCESSED
	wantPgCode(t, execErr(pool, `INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, result_balance, provider_id, created_at, updated_at)
		VALUES ($1, $2, $3, 'OPENING', 100, 'BRL', 'PROCESSED', 100, 'prov', now(), now())`, uuid4(), walletID, playerID), sqlCheckViolation, "OPENING com provedor")
	wantPgCode(t, execErr(pool, `INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'OPENING', 100, 'BRL', 'PENDING', now(), now())`, uuid4(), walletID, playerID), sqlCheckViolation, "OPENING pendente")
	// operação externa sem metadados
	wantPgCode(t, execErr(pool, `INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'BET', 100, 'BRL', 'PENDING', now(), now())`, uuid4(), walletID, playerID), sqlCheckViolation, "BET sem metadados externos")
}

func TestSchemaAtomicRollback(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	txID := insertExternalTx(t, pool, walletID, playerID, "BET", 2500, "e-"+uuid4())

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `UPDATE wallets SET balance = 7500, version = 2, updated_at = $2 WHERE id = $1`, walletID, time.Now()); err != nil {
		t.Fatal(err)
	}
	// o lançamento falha (aritmética errada): a transação inteira deve ser descartada
	if _, err := tx.Exec(context.Background(), `INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction, amount, balance_before, balance_after, created_at)
		VALUES ($1, $2, $3, 'DEBIT', 2500, 10000, 1, now())`, uuid4(), walletID, txID); err == nil {
		t.Fatal("esperava falha no lançamento")
	}
	_ = tx.Rollback(context.Background())

	var balance, version int64
	if err := pool.QueryRow(context.Background(), `SELECT balance, version FROM wallets WHERE id = $1`, walletID).Scan(&balance, &version); err != nil {
		t.Fatal(err)
	}
	if balance != 10000 || version != 1 {
		t.Fatalf("o rollback deveria preservar saldo e versão: %d v%d", balance, version)
	}
}

const pendingRefSQL = `
INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount, currency, status, result_balance, failure_code,
    provider_id, external_transaction_id, idempotency_key, request_hash, round_id, game_id, reference_external_transaction_id,
    reference_attempts, next_attempt_at, created_at, updated_at)
VALUES ($1, $2, $3, 'REFUND', 100, 'BRL', $4, $5, $6, 'prov', $7, $8, 'h', 'round', 'game', 'ref-ext', $9, $10, now(), now())`

func TestSchemaPendingReferenceScheduling(t *testing.T) {
	pool := newPool(t)
	walletID, playerID := insertWallet(t, pool, 10000)
	ins := func(status string, balance, failure any, attempts int, next any) error {
		ext := "e-" + uuid4()
		return execErr(pool, pendingRefSQL, uuid4(), walletID, playerID, status, balance, failure, ext, "k-"+ext, attempts, next)
	}

	// pendente exige a próxima tentativa; as demais proíbem
	wantPgCode(t, ins("PENDING_REFERENCE", nil, nil, 0, nil), sqlCheckViolation, "PENDING_REFERENCE sem next_attempt_at")
	wantPgCode(t, ins("PROCESSED", 100, nil, 0, time.Now()), sqlCheckViolation, "PROCESSED com next_attempt_at")
	wantPgCode(t, ins("REJECTED", nil, "X", 0, time.Now()), sqlCheckViolation, "REJECTED com next_attempt_at")
	wantPgCode(t, ins("PENDING_REFERENCE", nil, nil, -1, time.Now()), sqlCheckViolation, "tentativas negativas")

	if err := ins("PENDING_REFERENCE", nil, nil, 3, time.Now()); err != nil {
		t.Fatalf("pendência válida: %v", err)
	}
	if err := ins("PROCESSED", 100, nil, 2, nil); err != nil {
		t.Fatalf("concluída com tentativas registradas é válida: %v", err)
	}
}

func TestSchemaPendingReferenceIndexIsUsed(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// força o planejador a preferir índices, para verificar que o parcial atende à busca
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT id FROM wager_transactions WHERE status = 'PENDING_REFERENCE' AND next_attempt_at <= now() ORDER BY next_attempt_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if !strings.Contains(plan, "wager_transactions_pending_reference_idx") {
		t.Fatalf("o plano não usa o índice parcial:\n%s", plan)
	}
}
