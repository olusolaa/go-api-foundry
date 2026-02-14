# Design Patterns

This section summarizes the main design patterns used in this project and where they live.

## Implemented Patterns

### 1. Strategy Pattern - Rate Limiting
**Location:** `pkg/ratelimit/strategy.go`

The Strategy Pattern eliminates conditional logic for different rate limiting implementations (in-memory vs Redis-backed). The `RateLimiter` interface allows seamless switching between implementations without changing client code.

**Notes:**
- Clean separation of rate limiting algorithms
- Easy to add new rate limiting strategies
- Testable implementations
- Configurable at runtime

Implementation details:

- In-memory limiter is keyed per client (token bucket per key)
- Redis limiter uses a sliding-window approach with an atomic Lua script

**Usage:**
```go
// Create rate limiter using strategy pattern
config := &ratelimit.RateLimitConfig{
    Requests: 100,
    Window:   time.Minute,
    Redis:    redisClient, // optional
}
rateLimiter := ratelimit.NewRateLimiter(config)
```

### 2. Sentinel Errors - Domain Error Handling
**Location:** `domain/ledger/errors.go`

Domain errors are plain Go sentinel values (`var ErrXxx = errors.New(...)`) checked with `errors.Is`. HTTP status mapping happens at the controller boundary via a `mapDomainError` switch, keeping the domain layer free of HTTP concerns.

**Benefits:**
- Idiomatic Go error handling
- Domain layer has zero HTTP awareness
- `errors.Is` for reliable error matching
- Single mapping function at the controller boundary

**Usage:**
```go
// Domain layer returns sentinels
if source.Balance < amount {
    return ErrInsufficientFunds
}

// Controller maps at boundary
code, msg := mapDomainError(err)
return router.ErrorResult(code, msg, nil)
```

### 3. Double-Entry Bookkeeping - Financial Correctness
**Location:** `domain/ledger/repository.go`

Every monetary operation creates exactly one DEBIT and one CREDIT entry within a single database transaction. Deterministic lock ordering prevents deadlocks and idempotency keys ensure exactly-once processing.

**Guarantees:**
- Total debits always equal total credits (zero-sum)
- Pessimistic locking with `SELECT ... FOR UPDATE`
- Deterministic lock ordering via `slices.Sort` on account IDs
- Cached balances verified by reconciliation endpoint

## Integration Points

### Router Service
The router uses the Strategy Pattern for rate limiting:

- Environment-based configuration
- Automatic fallback to in-memory if Redis is not configured or unreachable

### Domain Layer
Each domain mounts its controller directly in `domain/main.go`:
- Repository, service, and controller instantiated inline
- Proper dependency injection via constructor functions
- Testable via interfaces and mock generation

## Configuration

All patterns support environment-based configuration:

```bash
# Rate Limiting
RATE_LIMIT_REQUESTS=100
RATE_LIMIT_WINDOW=1m

# Redis (optional)
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_PASSWORD=yourpassword
```

## Testing

Interfaces make mocking straightforward:

```go
// Mock rate limiter for testing
type mockRateLimiter struct{}
func (m *mockRateLimiter) GetLimitDetails() (int, time.Duration) { return 0, 0 }
func (m *mockRateLimiter) IsLimited(key string) (bool, error) { return false, nil }
func (m *mockRateLimiter) Close() error { return nil }

// Use in tests
rateLimiter = &mockRateLimiter{}
```

## Summary

1. **Maintainability:** Clean interfaces and separation of concerns
2. **Testability:** Dependency injection enables comprehensive testing
3. **Scalability:** Strategy pattern allows horizontal scaling with Redis
4. **Correctness:** Double-entry bookkeeping with reconciliation proves balances
5. **Idiomatic Go:** Sentinel errors, table-driven tests, minimal abstractions
