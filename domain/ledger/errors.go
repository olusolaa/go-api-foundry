package ledger

import "errors"

// Sentinel errors for the ledger domain.
var (
	ErrInsufficientFunds      = errors.New("insufficient funds")
	ErrCurrencyMismatch       = errors.New("currency mismatch between accounts")
	ErrAccountNotFound        = errors.New("account not found")
	ErrSelfTransfer           = errors.New("cannot transfer to the same account")
	ErrInvalidAmount          = errors.New("amount must be greater than zero")
	ErrIdempotencyConflict    = errors.New("idempotency key already used with different parameters")
	ErrSystemAccountForbidden = errors.New("operations on the system account are not allowed")
)
