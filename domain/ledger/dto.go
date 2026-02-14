package ledger

import (
	"github.com/akeren/go-api-foundry/internal/models"
	"github.com/akeren/go-api-foundry/pkg/constants"
)

// ========================================
// Request DTOs
// ========================================

type CreateAccountRequest struct {
	Name     string `json:"name" binding:"required,min=1,max=255"`
	Currency string `json:"currency" binding:"omitempty,len=3,uppercase"`
}

type DepositRequest struct {
	Amount         int64  `json:"amount" binding:"required,gt=0"`
	Currency       string `json:"currency" binding:"omitempty,len=3,uppercase"`
	IdempotencyKey string `json:"idempotency_key" binding:"required,min=1,max=255"`
	Description    string `json:"description" binding:"omitempty,max=500"`
}

type WithdrawRequest struct {
	Amount         int64  `json:"amount" binding:"required,gt=0"`
	Currency       string `json:"currency" binding:"omitempty,len=3,uppercase"`
	IdempotencyKey string `json:"idempotency_key" binding:"required,min=1,max=255"`
	Description    string `json:"description" binding:"omitempty,max=500"`
}

type TransferRequest struct {
	SourceAccountID string `json:"source_account_id" binding:"required,min=1"`
	DestAccountID   string `json:"dest_account_id" binding:"required,min=1"`
	Amount          int64  `json:"amount" binding:"required,gt=0"`
	Currency        string `json:"currency" binding:"omitempty,len=3,uppercase"`
	IdempotencyKey  string `json:"idempotency_key" binding:"required,min=1,max=255"`
	Description     string `json:"description" binding:"omitempty,max=500"`
}

// ========================================
// Response DTOs
// ========================================

type AccountResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccountType string `json:"account_type"`
	Currency    string `json:"currency"`
	Balance     int64  `json:"balance"`
	CreatedAt   string `json:"created_at"`
}

type TransactionResponse struct {
	ID              string               `json:"id"`
	IdempotencyKey  string               `json:"idempotency_key"`
	TransactionType string               `json:"transaction_type"`
	Amount          int64                `json:"amount"`
	Currency        string               `json:"currency"`
	Description     string               `json:"description"`
	Entries         []LedgerEntryResponse `json:"entries"`
	CreatedAt       string               `json:"created_at"`
}

type LedgerEntryResponse struct {
	ID           string `json:"id"`
	AccountID    string `json:"account_id"`
	EntryType    string `json:"entry_type"`
	Amount       int64  `json:"amount"`
	BalanceAfter int64  `json:"balance_after"`
	CreatedAt    string `json:"created_at"`
}

type BalanceResponse struct {
	AccountID      string `json:"account_id"`
	CachedBalance  int64  `json:"cached_balance"`
	DerivedBalance int64  `json:"derived_balance"`
	Currency       string `json:"currency"`
	IsConsistent   bool   `json:"is_consistent"`
}

type ReconciliationResponse struct {
	Accounts     []AccountReconciliation `json:"accounts"`
	AllConsistent bool                   `json:"all_consistent"`
}

// ========================================
// Mappers
// ========================================

func ToAccountModel(req *CreateAccountRequest) *models.Account {
	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}
	return &models.Account{
		Name:        req.Name,
		AccountType: models.AccountTypeUser,
		Currency:    currency,
	}
}

func ToAccountResponse(acc *models.Account) AccountResponse {
	return AccountResponse{
		ID:          acc.ID,
		Name:        acc.Name,
		AccountType: acc.AccountType,
		Currency:    acc.Currency,
		Balance:     acc.Balance,
		CreatedAt:   acc.CreatedAt.Format(constants.RFC3339DateTimeFormat),
	}
}

func ToTransactionResponse(txn *models.Transaction) TransactionResponse {
	entries := make([]LedgerEntryResponse, 0, len(txn.Entries))
	for _, e := range txn.Entries {
		entries = append(entries, ToLedgerEntryResponse(&e))
	}
	return TransactionResponse{
		ID:              txn.ID,
		IdempotencyKey:  txn.IdempotencyKey,
		TransactionType: txn.TransactionType,
		Amount:          txn.Amount,
		Currency:        txn.Currency,
		Description:     txn.Description,
		Entries:         entries,
		CreatedAt:       txn.CreatedAt.Format(constants.RFC3339DateTimeFormat),
	}
}

func ToLedgerEntryResponse(entry *models.LedgerEntry) LedgerEntryResponse {
	return LedgerEntryResponse{
		ID:           entry.ID,
		AccountID:    entry.AccountID,
		EntryType:    entry.EntryType,
		Amount:       entry.Amount,
		BalanceAfter: entry.BalanceAfter,
		CreatedAt:    entry.CreatedAt.Format(constants.RFC3339DateTimeFormat),
	}
}
