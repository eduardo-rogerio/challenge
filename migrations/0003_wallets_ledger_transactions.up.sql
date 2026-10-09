-- Carteiras: o saldo é o estado corrente; o ledger é a história.
CREATE TABLE wallets (
    id         uuid        PRIMARY KEY,
    player_id  uuid        NOT NULL,
    currency   char(3)     NOT NULL,
    balance    bigint      NOT NULL,
    version    bigint      NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT wallets_balance_non_negative CHECK (balance >= 0),
    CONSTRAINT wallets_version_positive     CHECK (version >= 1),
    CONSTRAINT wallets_currency_format      CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT wallets_player_currency_key  UNIQUE (player_id, currency)
);

-- Transações: operações externas de provedores e a abertura interna (OPENING).
CREATE TABLE wager_transactions (
    id                                uuid        PRIMARY KEY,
    wallet_id                         uuid        NOT NULL REFERENCES wallets (id),
    player_id                         uuid        NOT NULL,
    kind                              text        NOT NULL,
    amount                            bigint      NOT NULL,
    currency                          char(3)     NOT NULL,
    status                            text        NOT NULL,
    failure_code                      text,
    result_balance                    bigint,
    provider_id                       text,
    external_transaction_id           text,
    idempotency_key                   text,
    request_hash                      text,
    round_id                          text,
    game_id                           text,
    reference_external_transaction_id text,
    resolved_reference_id             uuid        REFERENCES wager_transactions (id),
    created_at                        timestamptz NOT NULL,
    updated_at                        timestamptz NOT NULL,
    CONSTRAINT wager_transactions_kind_valid   CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    CONSTRAINT wager_transactions_status_valid CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    -- LOSS não movimenta (valor zero); todos os demais exigem valor positivo.
    CONSTRAINT wager_transactions_amount_policy CHECK ((kind = 'LOSS' AND amount = 0) OR (kind <> 'LOSS' AND amount > 0)),
    -- OPENING é interna (sem metadados externos, já PROCESSED); as demais exigem todos.
    CONSTRAINT wager_transactions_origin_shape CHECK (
        (kind = 'OPENING'
            AND status = 'PROCESSED'
            AND provider_id IS NULL AND external_transaction_id IS NULL AND idempotency_key IS NULL
            AND request_hash IS NULL AND round_id IS NULL AND game_id IS NULL
            AND reference_external_transaction_id IS NULL)
        OR
        (kind <> 'OPENING'
            AND provider_id IS NOT NULL AND external_transaction_id IS NOT NULL AND idempotency_key IS NOT NULL
            AND request_hash IS NOT NULL AND round_id IS NOT NULL AND game_id IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_reversal_reference CHECK (
        kind NOT IN ('REFUND', 'ROLLBACK') OR reference_external_transaction_id IS NOT NULL
    ),
    -- failure_code existe se, e somente se, a transação foi rejeitada ou falhou.
    CONSTRAINT wager_transactions_failure_code CHECK ((status IN ('REJECTED', 'FAILED')) = (failure_code IS NOT NULL)),
    -- result_balance (saldo observado) existe se, e somente se, a transação foi processada.
    CONSTRAINT wager_transactions_result_balance CHECK (
        (status = 'PROCESSED') = (result_balance IS NOT NULL) AND (result_balance IS NULL OR result_balance >= 0)
    ),
    -- Uma operação externa não pode ser reaplicada.
    CONSTRAINT wager_transactions_external_key UNIQUE (provider_id, external_transaction_id)
);

CREATE INDEX wager_transactions_wallet_idx ON wager_transactions (wallet_id);

-- Uma única abertura por carteira.
CREATE UNIQUE INDEX wager_transactions_one_opening ON wager_transactions (wallet_id) WHERE kind = 'OPENING';

-- Uma única reversão bem-sucedida (REFUND ou ROLLBACK) por transação referenciada.
CREATE UNIQUE INDEX wager_transactions_one_reversal ON wager_transactions (resolved_reference_id)
    WHERE kind IN ('REFUND', 'ROLLBACK') AND status = 'PROCESSED';

-- Ledger append-only; seq dá a ordem estável usada na paginação.
CREATE TABLE wallet_ledger_entries (
    seq            bigint      GENERATED ALWAYS AS IDENTITY,
    id             uuid        PRIMARY KEY,
    wallet_id      uuid        NOT NULL REFERENCES wallets (id),
    transaction_id uuid        NOT NULL REFERENCES wager_transactions (id),
    direction      text        NOT NULL,
    amount         bigint      NOT NULL,
    balance_before bigint      NOT NULL,
    balance_after  bigint      NOT NULL,
    created_at     timestamptz NOT NULL,
    CONSTRAINT ledger_direction_valid  CHECK (direction IN ('DEBIT', 'CREDIT')),
    CONSTRAINT ledger_amount_positive  CHECK (amount > 0),
    CONSTRAINT ledger_balances_non_negative CHECK (balance_before >= 0 AND balance_after >= 0),
    CONSTRAINT ledger_arithmetic CHECK (
        (direction = 'CREDIT' AND balance_after = balance_before + amount)
        OR (direction = 'DEBIT' AND balance_after = balance_before - amount)
    ),
    CONSTRAINT ledger_wallet_transaction_key UNIQUE (wallet_id, transaction_id)
);

CREATE UNIQUE INDEX ledger_wallet_seq_idx ON wallet_ledger_entries (wallet_id, seq);

-- Correções financeiras exigem novos lançamentos: UPDATE, DELETE e TRUNCATE são proibidos.
CREATE FUNCTION wallet_ledger_entries_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries é append-only: % bloqueado', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$;

CREATE TRIGGER wallet_ledger_entries_no_update_delete
    BEFORE UPDATE OR DELETE ON wallet_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION wallet_ledger_entries_append_only();

CREATE TRIGGER wallet_ledger_entries_no_truncate
    BEFORE TRUNCATE ON wallet_ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION wallet_ledger_entries_append_only();
