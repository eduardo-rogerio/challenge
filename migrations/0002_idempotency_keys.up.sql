CREATE TABLE idempotency_keys (
    key                     text        PRIMARY KEY,
    provider_id             text        NOT NULL,
    external_transaction_id text        NOT NULL,
    request_hash            text        NOT NULL,
    state                   text        NOT NULL CHECK (state IN ('IN_PROGRESS', 'COMPLETED')),
    response                jsonb,
    locked_until            timestamptz NOT NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    -- Uma operação externa não pode ser reaplicada sob outra chave.
    CONSTRAINT idempotency_keys_operation_key UNIQUE (provider_id, external_transaction_id),
    -- O resultado existe se, e somente se, a chave foi concluída.
    CONSTRAINT idempotency_keys_response_check CHECK ((state = 'COMPLETED') = (response IS NOT NULL))
);
