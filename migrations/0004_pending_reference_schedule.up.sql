-- Agendamento das reversões em PENDING_REFERENCE: quantas tentativas já ocorreram e quando é a próxima.
ALTER TABLE wager_transactions
    ADD COLUMN reference_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at    timestamptz;

-- Pendências que já existiam ficam elegíveis imediatamente.
UPDATE wager_transactions SET next_attempt_at = updated_at WHERE status = 'PENDING_REFERENCE';

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_attempts_non_negative CHECK (reference_attempts >= 0),
    -- A próxima tentativa existe se, e somente se, a transação aguarda a referência.
    ADD CONSTRAINT wager_transactions_next_attempt CHECK ((status = 'PENDING_REFERENCE') = (next_attempt_at IS NOT NULL));

-- Busca das pendências vencidas.
CREATE INDEX wager_transactions_pending_reference_idx ON wager_transactions (next_attempt_at)
    WHERE status = 'PENDING_REFERENCE';
