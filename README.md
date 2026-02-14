# Go API Foundry — Double-Entry Ledger

A correct, auditable double-entry ledger built on the Go API Foundry starter kit. Gin + PostgreSQL (GORM) + optional Redis, with sane production defaults.

## Ledger Design

### Principles

- **Double-entry bookkeeping**: Every transaction creates exactly two ledger entries (one DEBIT, one CREDIT). Total debits always equal total credits across the entire system.
- **Amounts in cents (int64)**: No floating point. `10000` = $100.00. Standard financial practice.
- **Immutable audit trail**: Ledger entries cannot be updated or deleted (enforced by database trigger on PostgreSQL).
- **Idempotency**: Every mutation requires an `idempotency_key`. Replaying the same request returns the original result without double-processing.
- **Cached + derived balances**: Account balances are cached for O(1) reads, verified against derived balances (sum of ledger entries) via reconciliation.
- **System account**: A well-known "External Funding Source" account (UUID `00000000-0000-0000-0000-000000000001`) serves as the counterparty for deposits and withdrawals. System accounts can go negative; user accounts cannot.

### Data Model

```
accounts
├── id UUID PK
├── name TEXT NOT NULL
├── account_type TEXT NOT NULL (USER | SYSTEM)
├── currency CHAR(3) DEFAULT 'USD'
├── balance BIGINT DEFAULT 0        ← cached, verified by reconciliation
├── version BIGINT DEFAULT 0        ← optimistic concurrency
├── created_at, updated_at

transactions
├── id UUID PK
├── idempotency_key TEXT UNIQUE     ← exactly-once processing
├── transaction_type TEXT (DEPOSIT | WITHDRAWAL | TRANSFER)
├── amount BIGINT > 0
├── currency CHAR(3)
├── description TEXT
├── created_at

ledger_entries (immutable)
├── id UUID PK
├── transaction_id UUID FK → transactions
├── account_id UUID FK → accounts
├── entry_type TEXT (DEBIT | CREDIT)
├── amount BIGINT > 0
├── balance_after BIGINT            ← snapshot for O(1) audit
├── created_at
```

### Concurrency & Consistency

- **Pessimistic locking**: `SELECT ... FOR UPDATE` via GORM's `clause.Locking{Strength: "UPDATE"}`. On PostgreSQL this acquires row-level locks; on SQLite it's a no-op (single-writer serialization).
- **Deterministic lock ordering**: Account UUIDs are sorted lexicographically before locking to prevent deadlocks.
- **Atomic transactions**: All balance changes happen within a single database transaction. Either both entries + both balance updates commit, or nothing does.

### Trade-offs

| Decision | Why | Cost |
|----------|-----|------|
| **Pessimistic locking** (`SELECT ... FOR UPDATE`) | Simpler than optimistic locking (version check + retry loop). Correct under moderate write contention. | Holds row locks for the duration of the transaction. Under extreme contention, optimistic locking with retry would yield higher throughput. |
| **Cached balance on accounts** | O(1) balance reads without scanning ledger entries. | Redundant data that must stay in sync. Mitigated by the reconciliation endpoint which proves correctness. |
| **Single `ExecuteDoubleEntry` method** | Deposit, withdrawal, and transfer are all the same operation (debit source, credit destination). One code path = one place for bugs. | Less flexibility for operation-specific logic. Acceptable because the abstraction is mathematically exact. |
| **Idempotency check after lock acquisition** | Avoids PostgreSQL's "current transaction is aborted" problem that occurs when a UNIQUE constraint violation is handled with a fallback SELECT inside a transaction. | Holds row locks slightly longer (idempotency lookup happens while locks are held). |
| **System account can go negative** | Required for double-entry correctness — deposits must debit *something*. The system account is a bookkeeping counterparty, not real funds. | System account balance is a synthetic number. Operators must understand it represents net outflow, not a deficit. |
| **Amounts in cents (`int64`)** | Eliminates all IEEE 754 floating-point precision issues. Standard financial practice. | API consumers must convert to/from display format (divide by 100 for dollars). |
| **Sentinel errors at domain boundary** | Domain layer returns plain `errors.New` values. Controller maps to HTTP status codes via `errors.Is`. Domain stays HTTP-agnostic. | Mapping switch in the controller. Scales linearly with error count but stays co-located with handlers. |
| **SQLite for integration tests** | Zero infrastructure, fast, in-memory. `FOR UPDATE` is a no-op on SQLite, but SQLite serializes all writes anyway, so concurrency correctness is still tested. | Does not exercise PostgreSQL-specific locking semantics. Add a PostgreSQL CI service for full coverage. |
| **No partitioning/sharding** | Simple accounts table with row-level locks. Correct at moderate scale. | Would need horizontal partitioning for millions of concurrent accounts. The API contract does not change when this is added. |

### Future Optimizations

| Optimization | Impact | Complexity |
|-------------|--------|------------|
| **PostgreSQL in CI** | Exercises real `FOR UPDATE` locking and the immutability trigger. Currently only tested via SQLite. | Low — add a PostgreSQL service to GitHub Actions and run integration tests against it. |
| **Read replicas for balance queries** | Balance and transaction history reads don't need the primary. Offloading reduces lock contention on writes. | Medium — requires connection routing (primary for writes, replica for reads). |
| **Async reconciliation** | Reconciliation scans all accounts. At scale, this should be a background job with results cached, not a synchronous API call. | Medium — add a job scheduler (e.g., cron or a worker queue) and a reconciliation results table. |
| **Batch transfers** | Process multiple transfers in a single database transaction to amortize lock acquisition overhead. | Medium — new API endpoint, careful lock ordering across N accounts. |
| **Event sourcing** | Derive balances entirely from ledger entries, removing the cached balance. Eliminates reconciliation drift by design. | High — slower reads without materialized views or CQRS. Significant architectural change. |
| **Horizontal partitioning** | Shard accounts by UUID prefix or tenant ID for massive scale. | High — requires a sharding strategy, cross-shard transfer handling, and distributed reconciliation. |
| **Multi-currency transfers** | Support cross-currency transfers with exchange rate lookups and conversion entries. | High — adds FX rate sourcing, conversion ledger entries, and rounding rules. |

## API Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/v1/ledger/accounts` | Create user account |
| `GET` | `/v1/ledger/accounts/:id` | Get account details |
| `POST` | `/v1/ledger/accounts/:id/deposit` | Deposit (External Funding → User) |
| `POST` | `/v1/ledger/accounts/:id/withdraw` | Withdraw (User → External Funding) |
| `POST` | `/v1/ledger/transfers` | Transfer (User A → User B) |
| `GET` | `/v1/ledger/accounts/:id/balance` | Balance (cached + derived) |
| `GET` | `/v1/ledger/accounts/:id/transactions` | Transaction history with entries |
| `GET` | `/v1/ledger/reconciliation` | Verify all balances match |

### Example: Deposit $50.00

```bash
curl -X POST http://localhost:8080/v1/ledger/accounts/<account-id>/deposit \
  -H 'Content-Type: application/json' \
  -d '{"amount": 5000, "idempotency_key": "dep-001", "description": "Initial deposit"}'
```

### Example: Transfer $25.00

```bash
curl -X POST http://localhost:8080/v1/ledger/transfers \
  -H 'Content-Type: application/json' \
  -d '{
    "source_account_id": "<alice-id>",
    "dest_account_id": "<bob-id>",
    "amount": 2500,
    "idempotency_key": "xfr-001"
  }'
```

## Quick Start

### 1) Configure environment

```bash
cp .env.example .env
```

At minimum, set database + app port values.

### 2) Run with Docker Compose

```bash
docker-compose -f docker-compose.dev.yaml up --build -d
```

### 3) Run migrations

```bash
go run cmd/cli/main.go migrate
```

### 4) Start the server

```bash
go run cmd/server/main.go
```

Or with auto-migrate (development only):

```bash
go run cmd/server/main.go --auto-migrate
```

## Testing

### Unit tests (no database)

```bash
go test ./...
```

### Integration tests (SQLite in-memory)

```bash
RUN_INTEGRATION_TESTS=true go test ./integration/... -v
```

### Race detection

```bash
go test -race ./...
```

### Manual reconciliation (PostgreSQL)

```bash
psql -d your_database -f scripts/reconcile_ledger.sql
```

## Health, Metrics, and Headers

- `GET /health` — health check
- `GET /metrics` — Prometheus metrics (set `METRICS_ENABLED=false` to disable)
- Correlation ID: request/response header `X-Correlation-ID`

## Migrations

Versioned SQL migrations stored in `./migrations/`, applied via the CLI:

```bash
make migrate
```

## Project Structure

```
domain/ledger/
├── controller.go       # HTTP handlers + mapDomainError boundary
├── service.go          # Business logic + validation
├── repository.go       # Data access + double-entry execution
├── dto.go              # Request/response DTOs + mappers
├── errors.go           # Sentinel errors (var ErrXxx = errors.New(...))
├── mock_repository.go  # Generated mock (mockgen)
└── service_test.go     # Unit tests (32 cases, table-driven)

integration/
└── ledger_test.go      # Integration tests (21 cases, incl. concurrency)

migrations/
├── 000002_ledger.up.sql    # Schema + trigger + seed
└── 000002_ledger.down.sql  # Rollback

scripts/
└── reconcile_ledger.sql    # Manual reconciliation query
```

## License

Apache-2.0. See [LICENCE](LICENCE) and [NOTICE](NOTICE).
