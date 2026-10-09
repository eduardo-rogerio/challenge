# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

Go 1.26 service for the backend challenge at https://github.com/junglegaming/backend-challenge-go (distributed wager processing: HTTP API, SQS consumer, outbox workers, PostgreSQL). Built so far: the worker that resumes `PENDING_REFERENCE` reversals (retry/backoff/TTL, `FOR UPDATE SKIP LOCKED` claims with a lease), layout, local environment, health/metrics, the HTTP API (wallets, ledger, transactions, reconciliation) running on PostgreSQL with per-wallet locking, persistent idempotency, and the pure domain core. Not implemented yet: SQS consumer/inbox, outbox/events, Uber Fx and OIDC auth (see `ARCHITECTURE.md` for what is decided vs pending).

## Commands

Go is not installed on the host. Everything runs in Docker through the `dev` service (profile `tools`), wrapped by `make`:

- `make test` / `make test-race` / `make vet` / `make fmt` / `make tidy`
- `make test-integration` starts PostgreSQL and LocalStack, migrates, creates queues, then runs `go test -race -tags integration ./...`
- Single test: `docker compose run --rm dev go test ./internal/platform/config -run TestLoadDefaults`
- `make up` runs the full stack; `make migrate-up` / `make migrate-down` apply or revert migrations (golang-migrate, `migrations/`)

Keep the Go version equal in `go.mod` and the Dockerfile `GO_VERSION` argument.

## Architecture

- `internal/domain` must not import HTTP, AWS, pgx, Fx or `adapters`/`platform`/`bootstrap` (enforced by `internal/domain/deps_test.go`; `internal/app/deps_test.go` does the same for use cases). `internal/app` holds use cases and ports; `internal/adapters/{http,sqs,postgres}` implement them; `internal/platform` has config, logging, metrics, ids and clock; `internal/bootstrap` composes everything with Fx.
- Composition uses Uber Fx and lives only in `internal/bootstrap` (`fx.Module`s: database, messaging, observability, application, http; `bootstrap.API` and `bootstrap.Worker` combine them) and `cmd/*` (thin mains: load/validate config, `fx.New`, `bootstrap.Run`). `domain`, `app` and the adapters must not import Fx (tests enforce it for domain and app). To add a component: write a plain constructor, `fx.Provide` it in a module, and register `OnStart`/`OnStop` via `fx.Lifecycle` (hooks start in constructor order and stop in reverse, so dependencies close last); health checkers join the `group:"checkers"`. Fx logs go to the JSON logger (injection details only at DEBUG).
- `internal/domain` holds the pure financial core (`Money` as int64 minor units, `Wallet`, `WalletLedgerEntry`, `WagerTransaction` state machine, `Apply` rules). Dependencies (ids, `now`) come in as parameters. Business rejections are `REJECTED` transactions with a `FailureCode`, not errors.
- HTTP handlers (`internal/adapters/http`) only validate format and call the `app.Wallets`/`app.Transactions` ports; `cmd/api` injects `WalletService` and the idempotent `TransactionService` (`internal/app`), which use the `app.Store` ports implemented in `internal/adapters/postgres`. Financial invariants live in the schema (migration `0003`: CHECKs, unique indexes, append-only ledger triggers), and `Submit` locks the wallet row (`SELECT … FOR UPDATE`) inside one SQL transaction. Handler tests use fakes of those ports (`testutil_test.go`), one test file per endpoint.
- Idempotency is a decorator (`app.IdempotentTransactions`) over `app.Transactions`, backed by `postgres.IdempotencyStore` (`idempotency_keys`); `cmd/api` wires it around `app.TransactionService`. All DB integration tests live in the single package `internal/adapters/postgres` (build tag `integration`, run by `make test-integration`); they use random ids and never truncate (the ledger rejects TRUNCATE), except the idempotency store tests that truncate `idempotency_keys`. Keep new DB tests in that package so packages do not race on the shared database.
- Background workers live in `bootstrap.Worker` (executable `worker`, compose service `worker`): register new ones with `runInBackground` (cancel + wait before dependencies close; it waits only 90% of the stop deadline because Fx skips later OnStop hooks once the stop context expires). The pending-reference worker (`app.PendingReferenceResolver`) reuses `domain.Apply` and the shared `resolveReference`; its schedule (`reference_attempts`, `next_attempt_at`) is DB state and uses the injected `Clock`, so tests drive it with a fake clock. Integration tests run with `-p 1` and clear leftover `PENDING_REFERENCE` rows first (the test DB is shared, and a running worker claims any due pending).
- Migrations run only in the Compose `migrate` service, never from the app, so replicas do not race.
- Config comes from env vars (`internal/platform/config`); local values live in `.env.example`, which Compose loads.

## Workflow: OpenSpec (spec-driven)

Changes are planned and tracked with OpenSpec, using the `spec-driven` schema set in `openspec/config.yaml`. The `context:` and per-artifact `rules:` in that file are still commented out. Fill them in with the tech stack and conventions once they exist, because they are fed to the AI when it creates artifacts.

Layout:
- `openspec/specs/` holds the main specs, which describe the current system behavior.
- `openspec/changes/<name>/` holds one in-flight change with its proposal, design, delta specs and tasks.
- `openspec/changes/archive/` holds completed changes.

Slash commands (`.claude/commands/opsx/`) and matching skills (`.claude/skills/openspec-*`):
- `/opsx:explore` is a thinking-partner mode for investigating ideas before or during a change.
- `/opsx:propose` creates a change and generates all of its artifacts in one step.
- `/opsx:update` revises a change's planning artifacts and keeps them coherent. It never edits code.
- `/opsx:apply` implements the tasks from a change.
- `/opsx:sync` merges a change's delta specs into the main specs.
- `/opsx:archive` finalizes a completed change and moves it to the archive.

Plan with propose or update. Write code only through apply.
