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

1. **No partitioning/sharding**: Simple accounts table with row-level locks. Correct for the current scale; partitioning can be added later without changing the API.
2. **Single `ExecuteDoubleEntry` method**: Deposit, withdrawal, and transfer are all the same operation (debit source, credit destination). DRY and provably correct.
3. **SQLite for tests**: Integration tests use SQLite in-memory for speed. `FOR UPDATE` is a no-op on SQLite, which is acceptable since SQLite serializes all writes.

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
├── controller.go       # HTTP handlers
├── service.go          # Business logic
├── repository.go       # Data access + double-entry execution
├── dto.go              # Request/response DTOs + mappers
├── errors.go           # Domain error types
├── factory.go          # Dependency injection
├── mock_repository.go  # Generated mock for testing
└── service_test.go     # Unit tests (25 cases)

integration/
└── ledger_test.go      # Integration tests (13 cases)

migrations/
├── 000002_ledger.up.sql    # Schema + trigger + seed
└── 000002_ledger.down.sql  # Rollback

scripts/
└── reconcile_ledger.sql    # Manual reconciliation query
```

## License

Apache-2.0. See [LICENCE](LICENCE) and [NOTICE](NOTICE).
