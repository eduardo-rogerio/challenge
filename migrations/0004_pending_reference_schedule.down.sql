DROP INDEX IF EXISTS wager_transactions_pending_reference_idx;
ALTER TABLE wager_transactions
    DROP CONSTRAINT IF EXISTS wager_transactions_next_attempt,
    DROP CONSTRAINT IF EXISTS wager_transactions_attempts_non_negative,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS reference_attempts;
